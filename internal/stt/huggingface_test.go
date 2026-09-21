package stt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHub serves the three hub endpoints the port reads from canned JSON.
type fakeHub struct {
	repos    []string
	info     map[string]string // repo -> model info JSON
	tree     map[string]string // repo -> tree JSON
	status   map[string]int    // repo -> status for the info endpoint
	requests atomic.Int32
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	now := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		peak := f.peak.Load()
		if now <= peak || f.peak.CompareAndSwap(peak, now) {
			break
		}
	}

	if r.Header.Get("User-Agent") != hubUserAgent {
		http.Error(w, "no user agent", http.StatusBadRequest)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case path == "models":
		if r.URL.Query().Get("author") != catalogOrg {
			http.Error(w, "wrong author", http.StatusBadRequest)
			return
		}
		var list []map[string]string
		for _, repo := range f.repos {
			list = append(list, map[string]string{"id": repo})
		}
		json.NewEncoder(w).Encode(list)
	case strings.Contains(path, "/tree/"):
		repo := strings.TrimPrefix(path[:strings.Index(path, "/tree/")], "models/")
		body, ok := f.tree[repo]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	default:
		repo := strings.TrimPrefix(path, "models/")
		if code, ok := f.status[repo]; ok {
			http.Error(w, "nope", code)
			return
		}
		body, ok := f.info[repo]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}
}

func (f *fakeHub) start(t *testing.T) *hub {
	t.Helper()
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return &hub{api: server.URL, client: server.Client(), workers: hubWorkers}
}

const fakeSHA = "0123456789abcdef0123456789abcdef01234567"

func infoJSON(languages any, meta string) string {
	langs, _ := json.Marshal(languages)
	if meta == "" {
		meta = "{}"
	}
	return fmt.Sprintf(`{"sha":%q,"cardData":{"language":%s,"license":"apache-2.0","transcribe_cpp":%s}}`, fakeSHA, langs, meta)
}

func lfsFile(path string, size int64, sum string) string {
	if sum == "" {
		return fmt.Sprintf(`{"type":"file","path":%q,"size":%d}`, path, size)
	}
	return fmt.Sprintf(`{"type":"file","path":%q,"size":134,"lfs":{"oid":%q,"size":%d}}`, path, sum, size)
}

func treeJSON(files ...string) string {
	return "[" + strings.Join(append([]string{`{"type":"file","path":"README.md","size":100}`}, files...), ",") + "]"
}

func sumFor(seed string) string { return digest([]byte(seed)) }

func newFakeHub() *fakeHub {
	return &fakeHub{
		info:   map[string]string{},
		tree:   map[string]string{},
		status: map[string]int{},
	}
}

func (f *fakeHub) add(repo, info, tree string) {
	f.repos = append(f.repos, repo)
	f.info[repo] = info
	f.tree[repo] = tree
}

func TestFetchCatalogBuildsFromTheHub(t *testing.T) {
	f := newFakeHub()
	f.add("handy-computer/parakeet-unified-en-0.6b-gguf",
		infoJSON([]string{"en"}, `{"translate":false,"streaming":true,"lang_detect":false,"wer_librispeech":{"q8_0":3.99},"rtf_m1":{"cpu":12.5},"rtf_x86":{"cpu":7.47}}`),
		treeJSON(
			lfsFile("parakeet-unified-en-0.6b-F16.gguf", 1400<<20, sumFor("f16")),
			lfsFile("parakeet-unified-en-0.6b-Q8_0.gguf", 700<<20, sumFor("q8")),
		))
	f.add("handy-computer/SenseVoiceSmall-gguf",
		infoJSON("zh", `{"wer_aishell":{"q8_0":7.14},"rtf_x86":{"cpu":15.86}}`),
		treeJSON(lfsFile("SenseVoiceSmall-Q8_0.gguf", 300<<20, sumFor("sense"))))
	f.add("handy-computer/plain-gguf",
		infoJSON([]string{"en", "fr", "de"}, ""),
		treeJSON(lfsFile("plain-Q8_0.gguf", 100<<20, sumFor("plain"))))
	f.add("handy-computer/not-a-model", `{}`, `[]`)
	f.add("handy-computer/gone-gguf", "", "")
	f.status["handy-computer/gone-gguf"] = http.StatusNotFound

	catalog, err := f.start(t).catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 2 || catalog.Source != "https://huggingface.co/handy-computer" {
		t.Errorf("catalog header = %d %q", catalog.Version, catalog.Source)
	}
	if len(catalog.Models) != 3 {
		t.Fatalf("resolved %d models, want 3: %+v", len(catalog.Models), catalog.Models)
	}

	parakeet := catalog.Models[0]
	if parakeet.Slug != "parakeet-unified-en-0.6b" || parakeet.Rank != 1 || !parakeet.Recommended {
		t.Errorf("featured model not first with rank 1: %+v", parakeet)
	}
	if parakeet.Description != "Fast and accurate English. The best default if you dictate in English." {
		t.Errorf("featured description not applied: %q", parakeet.Description)
	}
	if parakeet.ID != "handy-computer/parakeet-unified-en-0.6b-gguf/parakeet-unified-en-0.6b-Q8_0.gguf" {
		t.Errorf("id = %q", parakeet.ID)
	}
	if parakeet.Quant != "Q8_0" || parakeet.Filename != "parakeet-unified-en-0.6b-Q8_0.gguf" {
		t.Errorf("Q8_0 should beat F16: %s %s", parakeet.Quant, parakeet.Filename)
	}
	if parakeet.SizeBytes != 700<<20 || parakeet.SHA256 != sumFor("q8") || parakeet.Revision != fakeSHA {
		t.Errorf("size/checksum/revision wrong: %+v", parakeet)
	}
	if parakeet.Name != "Parakeet Unified En 0.6b" || parakeet.License != "apache-2.0" {
		t.Errorf("name/license wrong: %q %q", parakeet.Name, parakeet.License)
	}
	if !parakeet.Streaming || parakeet.Translate || parakeet.LanguageDetect {
		t.Errorf("flags wrong: %+v", parakeet)
	}
	if parakeet.WordErrorRate == nil || *parakeet.WordErrorRate != 3.99 {
		t.Errorf("wer = %v, want 3.99", parakeet.WordErrorRate)
	}
	if parakeet.RealtimeFactor != 7.47 {
		t.Errorf("rtf = %v, want the slowest CPU figure 7.47", parakeet.RealtimeFactor)
	}
	if parakeet.AccuracyScore != 0.867 || parakeet.SpeedScore != 0.18675 {
		t.Errorf("scores = %v %v, want 0.867 0.18675", parakeet.AccuracyScore, parakeet.SpeedScore)
	}

	sense := catalog.Models[1]
	if sense.Rank != 5 || sense.Name != "Sense Voice Small" || len(sense.Languages) != 1 || sense.Languages[0] != "zh" {
		t.Errorf("a single language string should become a list: %+v", sense)
	}

	plain := catalog.Models[2]
	if plain.Rank != 999 || plain.Recommended || plain.Description != "Plain, 3 languages." {
		t.Errorf("unfeatured model curation wrong: %+v", plain)
	}
	if plain.WordErrorRate != nil || plain.RealtimeFactor != 0 || plain.AccuracyScore != 0.5 || plain.SpeedScore != 0.5 {
		t.Errorf("unmeasured model should score 0.5 with no rate: %+v", plain)
	}
}

func TestFetchCatalogSkipsWhatCannotBeVerifiedOrFit(t *testing.T) {
	f := newFakeHub()
	f.add("handy-computer/nosum-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("nosum-Q8_0.gguf", 10<<20, "")))
	f.add("handy-computer/toobig-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("toobig-Q8_0.gguf", 5<<30, sumFor("big")), lfsFile("toobig-F16.gguf", 9<<30, sumFor("bigger"))))
	f.add("handy-computer/nogguf-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("weights.bin", 10<<20, sumFor("bin"))))
	f.add("handy-computer/ok-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("ok-Q8_0.gguf", 10<<20, sumFor("ok"))))

	catalog, err := f.start(t).catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].Slug != "ok" {
		t.Errorf("only the verifiable model under the cap should remain: %+v", catalog.Models)
	}
	if catalog.Models[0].Description != "Ok, en only." {
		t.Errorf("single-language description = %q", catalog.Models[0].Description)
	}
}

func TestFetchCatalogAbortsOnServerErrors(t *testing.T) {
	f := newFakeHub()
	f.add("handy-computer/ok-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("ok-Q8_0.gguf", 10<<20, sumFor("ok"))))
	f.add("handy-computer/flaky-gguf", "", "")
	f.status["handy-computer/flaky-gguf"] = http.StatusBadGateway

	if _, err := f.start(t).catalog(context.Background()); err == nil {
		t.Fatal("a 5xx on one repo must abort rather than silently drop the model")
	}

	if _, err := newFakeHub().start(t).catalog(context.Background()); err == nil {
		t.Fatal("an empty org must not produce an empty catalogue")
	}
}

func TestFetchCatalogBoundsConcurrencyAndHonoursContext(t *testing.T) {
	f := newFakeHub()
	for i := 0; i < 30; i++ {
		repo := fmt.Sprintf("handy-computer/m%02d-gguf", i)
		f.add(repo, infoJSON([]string{"en"}, ""), treeJSON(lfsFile(fmt.Sprintf("m%02d-Q8_0.gguf", i), 10<<20, sumFor(repo))))
	}
	h := f.start(t)
	h.workers = 3

	catalog, err := h.catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 30 {
		t.Errorf("resolved %d, want 30", len(catalog.Models))
	}
	if peak := f.peak.Load(); peak > 3 {
		t.Errorf("%d requests in flight, want at most the worker count", peak)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.catalog(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context should surface, got %v", err)
	}
}

func TestPickQuantFallsBackUnderTheCap(t *testing.T) {
	files := []treeEntry{
		{Path: "v-F16.gguf", LFS: lfs("a", 9<<30)},
		{Path: "v-Q8_0.gguf", LFS: lfs("b", 4700<<20)},
		{Path: "v-Q5_K_M.gguf", LFS: lfs("c", 3200<<20)},
		{Path: "v-Q4_K_M.gguf", LFS: lfs("d", 2500<<20)},
	}
	quant, chosen := pickQuant(files, maxModelBytes)
	if quant != "Q5_K_M" || chosen.LFS.OID != "c" {
		t.Errorf("picked %s, want the best quantisation under 4 GB (Q5_K_M)", quant)
	}

	quant, chosen = pickQuant(files, 0)
	if quant != "Q8_0" || chosen.LFS.OID != "b" {
		t.Errorf("with no cap Q8_0 should win, got %s", quant)
	}

	if quant, chosen := pickQuant(files[:1], maxModelBytes); chosen != nil {
		t.Errorf("nothing under the cap should yield nothing, got %s", quant)
	}

	// The plain size is the LFS pointer's, so the LFS size wins when present;
	// a file with neither size is not excluded, matching the script.
	sized := []treeEntry{{Path: "x-Q8_0.gguf", Size: 134, LFS: lfs("e", 5<<30)}, {Path: "x-F16.gguf", Size: 0}}
	if quant, _ := pickQuant(sized, maxModelBytes); quant != "F16" {
		t.Errorf("LFS size should exclude Q8_0 and the unsized F16 should pass, got %s", quant)
	}

	// Matching is case-insensitive and only on the "-<quant>.gguf" suffix.
	if quant, _ := pickQuant([]treeEntry{{Path: "sub/model-q8_0.GGUF"}, {Path: "model-Q8_0.gguf.txt"}}, 0); quant != "Q8_0" {
		t.Errorf("suffix matching failed: %q", quant)
	}
}

func lfs(oid string, size int64) *struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
} {
	return &struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	}{OID: oid, Size: size}
}

func TestScoreMath(t *testing.T) {
	wer := func(v float64) *float64 { return &v }

	cases := []struct {
		wer  *float64
		want float64
	}{
		{wer(0), 1},
		{wer(3.99), 0.867},
		{wer(15), 0.5},
		{wer(62.2), 0.5}, // past the limit the script's `or 0.5` turns 0 into the unmeasured default
		{nil, 0.5},
	}
	for _, c := range cases {
		if got := orHalf(scoreFromWER(c.wer)); got != c.want {
			t.Errorf("accuracy for wer %v = %v, want %v", c.wer, got, c.want)
		}
	}

	for rtf, want := range map[float64]float64{0: 0.5, 7.47: 0.18675, 40: 1, 76.34: 1} {
		if got := orHalf(scoreFromRTF(rtf)); got != want {
			t.Errorf("speed for rtf %v = %v, want %v", rtf, got, want)
		}
	}
}

func TestBestWERPrefersTheChosenQuantThenTheFirstFigure(t *testing.T) {
	meta, err := orderedObject(json.RawMessage(`{
		"schema_version": 2,
		"wer_note": "not a table",
		"wer_fleurs_af": {"q8_0": 62.2, "q5_k_m": 63.1},
		"wer_fleurs_en": {"q8_0": 6.51}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := bestWER(meta, "Q5_K_M"); got == nil || *got != 63.1 {
		t.Errorf("chosen quant's figure = %v, want 63.1", got)
	}
	// The first table wins even when it lacks the quant: the script read the
	// card in order, and the shipped file was built that way.
	if got := bestWER(meta, "Q6_K"); got == nil || *got != 62.2 {
		t.Errorf("first figure fallback = %v, want 62.2", got)
	}
	if got := bestWER(nil, "Q8_0"); got != nil {
		t.Errorf("no metadata should give no rate, got %v", *got)
	}
	if got := bestWER(must(orderedObject(json.RawMessage(`{"wer_x": {}}`))), "Q8_0"); got != nil {
		t.Errorf("an empty table should give no rate, got %v", *got)
	}
}

func must(fields orderedFields, err error) orderedFields {
	if err != nil {
		panic(err)
	}
	return fields
}

func TestCPURTFTakesTheSlowestMachine(t *testing.T) {
	meta := must(orderedObject(json.RawMessage(`{
		"rtf_m1": {"cpu": 30.1, "gpu": 90},
		"rtf_x86": {"cpu": 7.47},
		"rtf_arm": {"gpu": 5},
		"rtf_broken": "n/a"
	}`)))
	if got := cpuRTF(meta); got != 7.47 {
		t.Errorf("rtf = %v, want 7.47", got)
	}
	if got := cpuRTF(nil); got != 0 {
		t.Errorf("no metadata should give 0, got %v", got)
	}
}

func TestTruthyFollowsPython(t *testing.T) {
	for raw, want := range map[string]bool{
		"true": true, "false": false, "null": false, "": false,
		"1": true, "0": false, `"yes"`: true, `""`: false,
		"[1]": true, "[]": false, `{"a":1}`: true, "{}": false,
	} {
		if got := truthy(json.RawMessage(raw)); got != want {
			t.Errorf("truthy(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"SenseVoiceSmall":                        "Sense Voice Small",
		"parakeet-unified-en-0.6b":               "Parakeet Unified En 0.6b",
		"diar_streaming_sortformer_4spk-v2.1":    "Diar Streaming Sortformer 4spk v2.1",
		"whisper-large-v3-turbo":                 "Whisper Large v3 Turbo",
		"granite-speech-5.0-470m-turboctc":       "Granite Speech 5.0 470m Turboctc",
		"Qwen3-ASR-1.7B":                         "Qwen3 ASR 1.7B",
		"multitalker-parakeet-streaming-0.6b-v1": "Multitalker Parakeet Streaming 0.6b v1",
		"MedASR":                                 "Med ASR",
		"eSpeak-NG":                              "e Speak NG",
		"medasr":                                 "Medasr",
	}
	for slug, want := range cases {
		if got := displayName(slug); got != want {
			t.Errorf("displayName(%q) = %q, want %q", slug, got, want)
		}
	}
}

// The generator must write what the shipped file holds, key for key.
func TestEncodeCatalogRoundTripsTheShippedFile(t *testing.T) {
	shipped, err := parseCatalog(embeddedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeCatalog(shipped)
	if err != nil {
		t.Fatal(err)
	}

	var want, got any
	if err := json.Unmarshal(embeddedCatalog, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	wantModels := want.(map[string]any)["models"].([]any)
	gotModels := got.(map[string]any)["models"].([]any)
	if len(wantModels) != len(gotModels) {
		t.Fatalf("model count changed: %d -> %d", len(wantModels), len(gotModels))
	}
	byID := map[string]any{}
	for _, m := range gotModels {
		byID[m.(map[string]any)["id"].(string)] = m
	}
	for _, m := range wantModels {
		entry := m.(map[string]any)
		encoded, ok := byID[entry["id"].(string)].(map[string]any)
		if !ok {
			t.Fatalf("%s missing after round trip", entry["id"])
		}
		for key, value := range entry {
			if fmt.Sprint(encoded[key]) != fmt.Sprint(value) {
				t.Errorf("%s.%s = %v, want %v", entry["slug"], key, encoded[key], value)
			}
		}
		if len(encoded) != len(entry) {
			t.Errorf("%s has %d keys after round trip, want %d", entry["slug"], len(encoded), len(entry))
		}
	}
	if !strings.HasPrefix(string(data), "{\n  \"catalog_version\": 2,\n  \"source\":") {
		t.Errorf("output is not pretty-printed in the shipped layout: %.60q", data)
	}
}

// resetCatalog forgets the active list so the next Models() reloads from disk.
func resetCatalog() {
	catalogMu.Lock()
	active = nil
	catalogMu.Unlock()
}

// useTempHome caches into a scratch directory so Refresh never touches the
// real home, and restores the embedded list for the tests that follow.
func useTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := cachePath
	cachePath = func() string { return filepath.Join(dir, "catalog.json") }
	resetCatalog()
	t.Cleanup(func() {
		cachePath = previous
		resetCatalog()
	})
	return dir
}

func redirectHub(t *testing.T, api string) {
	t.Helper()
	previous := hubAPI
	hubAPI = api
	t.Cleanup(func() { hubAPI = previous })
}

func TestRefreshAdoptsTheLiveCatalogAndCachesIt(t *testing.T) {
	dir := useTempHome(t)
	f := newFakeHub()
	f.add("handy-computer/fresh-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("fresh-Q8_0.gguf", 10<<20, sumFor("fresh"))))
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	redirectHub(t, server.URL)

	if !stale() {
		t.Fatal("the embedded catalogue should count as stale")
	}
	if err := Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := Models(); got.origin != "cached" || len(got.Models) != 1 || got.Models[0].Slug != "fresh" {
		t.Errorf("active catalogue was not replaced: %+v", got)
	}
	if stale() {
		t.Error("a just-refreshed catalogue is not stale")
	}

	cached, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cached), `"slug": "fresh"`) {
		t.Errorf("cache does not hold the fetched list: %.200s", cached)
	}

	// A fresh process reads the cache back in preference to the shipped copy.
	resetCatalog()
	if got := Models(); got.origin != "cached" || got.Models[0].Slug != "fresh" {
		t.Errorf("cache was not loaded on the next start: %+v", got)
	}
}

func TestRefreshKeepsTheCurrentListWhenTheHubFails(t *testing.T) {
	useTempHome(t)
	before := len(Models().Models)

	redirectHub(t, "http://127.0.0.1:1/unreachable")
	if err := Refresh(context.Background()); err == nil {
		t.Fatal("an unreachable hub must report failure")
	}

	// Reachable but useless: an org with nothing verifiable in it.
	f := newFakeHub()
	f.add("handy-computer/nosum-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("nosum-Q8_0.gguf", 10<<20, "")))
	empty := httptest.NewServer(f)
	t.Cleanup(empty.Close)
	redirectHub(t, empty.URL)
	if err := Refresh(context.Background()); err == nil {
		t.Fatal("a build with no usable model must report failure")
	}

	if got := Models(); got.origin != "embedded" || len(got.Models) != before {
		t.Errorf("the shipped list should survive a failed refresh: %s %d", got.origin, len(got.Models))
	}
}

func TestListenersHearSuccessfulRefreshesOnly(t *testing.T) {
	useTempHome(t)
	f := newFakeHub()
	f.add("handy-computer/heard-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("heard-Q8_0.gguf", 10<<20, sumFor("heard"))))
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)

	calls := 0
	unsubscribe := OnCatalogChanged(func() { calls++ })
	t.Cleanup(unsubscribe)

	redirectHub(t, server.URL)
	if err := Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("listener ran %d times after one refresh, want 1", calls)
	}

	redirectHub(t, "http://127.0.0.1:1/unreachable")
	if err := Refresh(context.Background()); err == nil {
		t.Fatal("an unreachable hub must report failure")
	}
	if calls != 1 {
		t.Errorf("a failed refresh must not notify, listener ran %d times", calls)
	}

	unsubscribe()
	redirectHub(t, server.URL)
	if err := Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("an unsubscribed listener still ran, %d calls", calls)
	}
}

func TestSchedulerRefreshesWhileStaleAndStops(t *testing.T) {
	useTempHome(t)
	f := newFakeHub()
	f.add("handy-computer/tick-gguf", infoJSON([]string{"en"}, ""),
		treeJSON(lfsFile("tick-Q8_0.gguf", 10<<20, sumFor("tick"))))
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	redirectHub(t, server.URL)

	startRefreshing(20 * time.Millisecond)
	t.Cleanup(StopRefreshing)

	deadline := time.Now().Add(5 * time.Second)
	for Models().origin != "cached" {
		if time.Now().After(deadline) {
			t.Fatal("the scheduler never refreshed a stale catalogue")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Once fresh, further ticks must not hit the hub again.
	time.Sleep(100 * time.Millisecond)
	StopRefreshing()
	seen := f.requests.Load()
	if seen != 3 {
		t.Errorf("hub saw %d requests, want exactly one list + info + tree round", seen)
	}

	time.Sleep(60 * time.Millisecond)
	if f.requests.Load() != seen {
		t.Error("the scheduler kept running after StopRefreshing")
	}

	// Starting twice is harmless and stopping twice does not block.
	startRefreshing(time.Hour)
	startRefreshing(time.Hour)
	StopRefreshing()
	StopRefreshing()
}
