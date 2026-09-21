package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/paradoxe35/encre/internal/logger"
)

// The catalogue is built from the handy-computer org on Hugging Face.
// Everything factual (size, checksum, languages, word error rate, realtime
// factor) comes from the hub API; only the curation below is ours.
const (
	HuggingFaceAPI = "https://huggingface.co/api"
	catalogOrg     = "handy-computer"
	catalogVersion = 2
	catalogSource  = "https://huggingface.co/" + catalogOrg

	// Too large to be a sensible download for a dictation tool.
	maxModelBytes = 4 << 30

	// Enough to hide latency without tripping the hub's rate limit.
	hubWorkers        = 6
	hubRequestTimeout = 30 * time.Second
	hubUserAgent      = "encre-catalog"
)

// Tests point this at a local server.
var hubAPI = HuggingFaceAPI

// Q8_0 first: near-lossless and still small for speech models. F32 last: it
// is the unquantised original, the largest file for no accuracy gain.
var quantPreference = []string{"Q8_0", "Q5_K_M", "Q6_K", "Q4_K_M", "F16", "F32"}

type featured struct {
	rank        int
	description string
}

// Offered first, with copy written for someone choosing rather than
// benchmarking.
var featuredModels = map[string]featured{
	"parakeet-unified-en-0.6b": {1, "Fast and accurate English. The best default if you dictate in English."},
	"whisper-small":            {2, "Multilingual workhorse. Good accuracy at moderate cost."},
	"whisper-tiny":             {3, "Smallest multilingual model. Runs anywhere, least accurate."},
	"canary-180m-flash":        {4, "English, German, Spanish and French, with translation."},
	"SenseVoiceSmall":          {5, "Chinese, Cantonese, English, Japanese and Korean."},
	"whisper-medium":           {6, "Higher accuracy, noticeably slower on CPU."},
}

// hub is one Hugging Face API root and the client used against it.
type hub struct {
	api     string
	client  *http.Client
	workers int
}

func newHub() *hub {
	return &hub{
		api:     hubAPI,
		client:  &http.Client{Timeout: hubRequestTimeout},
		workers: hubWorkers,
	}
}

// FetchCatalog builds the catalogue live from Hugging Face. A repo the hub no
// longer serves is skipped; any other failure aborts, since a half-built list
// would silently drop models the user may already have downloaded.
func FetchCatalog(ctx context.Context) (*Catalog, error) {
	return newHub().catalog(ctx)
}

// EncodeCatalog pretty-prints in the layout models.json is shipped in.
func EncodeCatalog(catalog *Catalog) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(catalog); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type hubStatusError struct {
	url    string
	status int
}

func (e *hubStatusError) Error() string {
	return fmt.Sprintf("%s returned HTTP %d", e.url, e.status)
}

// gone is a repo the hub will not serve: removed, private or gated. That is
// a fact about the repo, not the network, so the rest of the list still counts.
func (e *hubStatusError) gone() bool {
	return e.status == http.StatusNotFound || e.status == http.StatusForbidden || e.status == http.StatusUnauthorized
}

func (h *hub) get(ctx context.Context, path string, out any) error {
	target := h.api + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", hubUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &hubStatusError{url: target, status: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, catalogMaxLen)).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	return nil
}

func (h *hub) catalog(ctx context.Context) (*Catalog, error) {
	var listed []struct {
		ID string `json:"id"`
	}
	if err := h.get(ctx, "/models?author="+catalogOrg+"&limit=500", &listed); err != nil {
		return nil, fmt.Errorf("listing %s: %w", catalogOrg, err)
	}

	var repos []string
	for _, repo := range listed {
		if strings.HasSuffix(repo.ID, "-gguf") {
			repos = append(repos, repo.ID)
		}
	}
	sort.Strings(repos)

	// Every repo needs two round trips; a bounded pool keeps them overlapping
	// without hammering the hub. The first failure cancels the rest.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	resolved := make([]*Model, len(repos))
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, max(h.workers, 1))
		errMu    sync.Mutex
		firstErr error
	)
	for i, repo := range repos {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			model, err := h.resolve(ctx, repo)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				cancel()
				return
			}
			resolved[i] = model
		})
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	models := make([]Model, 0, len(resolved))
	for _, model := range resolved {
		if model != nil {
			models = append(models, *model)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("no models resolved")
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Rank != models[j].Rank {
			return models[i].Rank < models[j].Rank
		}
		return models[i].AccuracyScore > models[j].AccuracyScore
	})

	return &Catalog{Version: catalogVersion, Source: catalogSource, Models: models}, nil
}

type repoInfo struct {
	SHA      string `json:"sha"`
	CardData struct {
		Language      json.RawMessage `json:"language"`
		License       json.RawMessage `json:"license"`
		TranscribeCPP json.RawMessage `json:"transcribe_cpp"`
	} `json:"cardData"`
}

type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

// The LFS size is the real file; the plain size is the pointer's when the
// listing is not expanded.
func (e treeEntry) fileSize() int64 {
	if e.LFS != nil && e.LFS.Size != 0 {
		return e.LFS.Size
	}
	return e.Size
}

// resolve turns one repo into a catalogue entry. A nil model with a nil error
// means the repo was skipped deliberately.
func (h *hub) resolve(ctx context.Context, repo string) (*Model, error) {
	slug := strings.TrimSuffix(repo[strings.LastIndex(repo, "/")+1:], "-gguf")

	var info repoInfo
	if err := h.get(ctx, "/models/"+repo, &info); err != nil {
		if skippable(err) {
			logger.Info("Skipping a model the hub no longer serves", "model", slug, "reason", err)
			return nil, nil
		}
		return nil, err
	}

	var files []treeEntry
	if err := h.get(ctx, "/models/"+repo+"/tree/"+url.PathEscape(info.SHA)+"?recursive=1", &files); err != nil {
		if skippable(err) {
			logger.Info("Skipping a model whose files cannot be listed", "model", slug, "reason", err)
			return nil, nil
		}
		return nil, err
	}

	quant, chosen := pickQuant(files, maxModelBytes)
	if chosen == nil {
		logger.Info("Skipping a model with no quantisation under the size cap", "model", slug)
		return nil, nil
	}
	// The checksum is the trust anchor for a downloaded file; an entry without
	// one could never be verified, so it is left out rather than guessed.
	if chosen.LFS == nil || chosen.LFS.OID == "" {
		logger.Info("Skipping a model with no published checksum", "model", slug)
		return nil, nil
	}

	meta, err := orderedObject(info.CardData.TranscribeCPP)
	if err != nil {
		return nil, fmt.Errorf("%s: transcribe_cpp metadata: %w", repo, err)
	}
	languages := cardLanguages(info.CardData.Language)
	wer := bestWER(meta, quant)
	rtf := cpuRTF(meta)

	name := displayName(slug)
	curated, isFeatured := featuredModels[slug]
	description := curated.description
	if !isFeatured {
		switch {
		case len(languages) == 1:
			description = fmt.Sprintf("%s, %s only.", name, languages[0])
		case len(languages) > 0:
			description = fmt.Sprintf("%s, %d languages.", name, len(languages))
		default:
			description = name
		}
	}
	rank := 999
	if isFeatured {
		rank = curated.rank
	}

	return &Model{
		ID:             repo + "/" + chosen.Path,
		Slug:           slug,
		Name:           name,
		Description:    description,
		Repo:           repo,
		Revision:       info.SHA,
		Filename:       chosen.Path,
		Quant:          quant,
		SizeBytes:      chosen.fileSize(),
		SHA256:         chosen.LFS.OID,
		Languages:      languages,
		License:        rawString(info.CardData.License),
		Translate:      truthy(meta.get("translate")),
		Streaming:      truthy(meta.get("streaming")),
		LanguageDetect: truthy(meta.get("lang_detect")),
		WordErrorRate:  wer,
		RealtimeFactor: rtf,
		SpeedScore:     orHalf(scoreFromRTF(rtf)),
		AccuracyScore:  orHalf(scoreFromWER(wer)),
		Recommended:    isFeatured,
		Rank:           rank,
	}, nil
}

func skippable(err error) bool {
	var status *hubStatusError
	return errors.As(err, &status) && status.gone()
}

// pickQuant is the best quantisation that fits under the size cap. A repo is
// only out of reach when none of its quantisations fit: Voxtral's Q8_0 is
// 4.7 GB but its Q5_K_M is 3.2 GB at 0.04 more WER.
func pickQuant(files []treeEntry, maxBytes int64) (string, *treeEntry) {
	byQuant := make(map[string]*treeEntry, len(quantPreference))
	for i := range files {
		path := strings.ToUpper(files[i].Path)
		if !strings.HasSuffix(path, ".GGUF") {
			continue
		}
		for _, quant := range quantPreference {
			if strings.HasSuffix(path, "-"+quant+".GGUF") {
				byQuant[quant] = &files[i]
			}
		}
	}
	for _, quant := range quantPreference {
		chosen := byQuant[quant]
		if chosen == nil {
			continue
		}
		if size := chosen.fileSize(); maxBytes > 0 && size > 0 && size > maxBytes {
			continue
		}
		return quant, chosen
	}
	return "", nil
}

// WER of 0 is perfect; 30% is unusable. Clamped into 0..1.
func scoreFromWER(wer *float64) *float64 {
	if wer == nil {
		return nil
	}
	score := math.Max(0, math.Min(1, 1-*wer/30))
	return &score
}

// Real-time factor: 40x and above reads as full marks.
func scoreFromRTF(rtf float64) *float64 {
	if rtf == 0 {
		return nil
	}
	score := math.Max(0, math.Min(1, rtf/40))
	return &score
}

// orHalf is the unmeasured default. A zero score also reads as unmeasured,
// which keeps parity with the shipped file, where a model past the usable
// WER limit still shows 0.5 rather than 0.
func orHalf(score *float64) float64 {
	if score == nil || *score == 0 {
		return 0.5
	}
	return *score
}

// bestWER reads the first published error rate, preferring the figure for
// the chosen quantisation over whatever was measured first. First means
// first in the card, so key order matters.
func bestWER(meta orderedFields, quant string) *float64 {
	for _, field := range meta {
		if !strings.HasPrefix(field.key, "wer_") || !isObject(field.value) {
			continue
		}
		byQuant, err := orderedObject(field.value)
		if err != nil {
			return nil
		}
		if v, ok := number(byQuant.get(strings.ToLower(quant))); ok && v != 0 {
			return &v
		}
		if len(byQuant) > 0 {
			if v, ok := number(byQuant[0].value); ok {
				return &v
			}
		}
		return nil
	}
	return nil
}

// cpuRTF is the slowest measured CPU machine. Ranking should under-promise,
// not over. Zero means none published.
func cpuRTF(meta orderedFields) float64 {
	slowest := 0.0
	for _, field := range meta {
		if !strings.HasPrefix(field.key, "rtf_") || !isObject(field.value) {
			continue
		}
		var machines map[string]json.RawMessage
		if err := json.Unmarshal(field.value, &machines); err != nil {
			continue
		}
		if v, ok := number(machines["cpu"]); ok && v != 0 && (slowest == 0 || v < slowest) {
			slowest = v
		}
	}
	return slowest
}

// The card lists a single language as a string and several as an array.
func cardLanguages(raw json.RawMessage) []string {
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	if one := rawString(raw); one != "" {
		return []string{one}
	}
	return nil
}

func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func number(raw json.RawMessage) (float64, bool) {
	var v float64
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return 0, false
	}
	return v, true
}

func isObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// truthy follows Python's idea of truth, since the card's flags were written
// for a script that used bool().
func truthy(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case 't':
		return true
	case '"':
		return rawString(raw) != ""
	case '{', '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil {
			return len(items) > 0
		}
		fields, err := orderedObject(raw)
		return err == nil && len(fields) > 0
	default:
		v, ok := number(raw)
		return ok && v != 0
	}
}

type orderedField struct {
	key   string
	value json.RawMessage
}

// orderedFields is a JSON object with its key order kept; the script picked
// the first matching key, so a map would change which figure is used.
type orderedFields []orderedField

func (f orderedFields) get(key string) json.RawMessage {
	for _, field := range f {
		if field.key == key {
			return field.value
		}
	}
	return nil
}

func orderedObject(raw json.RawMessage) (orderedFields, error) {
	if !isObject(raw) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	var fields orderedFields
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, orderedField{key: key, value: value})
	}
	return fields, nil
}

// displayName turns a repo slug into something a list can show.
func displayName(slug string) string {
	var words []string
	for chunk := range strings.SplitSeq(strings.ReplaceAll(slug, "_", "-"), "-") {
		runes := []rune(chunk)
		switch {
		case len(runes) > 1 && hasUpper(runes[1:]):
			words = append(words, splitCamel(runes)...)
		case isUpper(runes) || hasDigit(runes):
			words = append(words, chunk)
		default:
			words = append(words, capitalize(runes))
		}
	}
	return strings.Join(words, " ")
}

// splitCamel: SenseVoiceSmall -> Sense Voice Small. A capital after a digit
// is a unit, not a new word, so 3B stays 3B.
func splitCamel(word []rune) []string {
	var out []string
	var current []rune
	for _, r := range word {
		if unicode.IsUpper(r) && len(current) > 0 {
			last := current[len(current)-1]
			if !unicode.IsUpper(last) && !unicode.IsDigit(last) {
				out = append(out, string(current))
				current = []rune{r}
				continue
			}
		}
		current = append(current, r)
	}
	if len(current) > 0 {
		out = append(out, string(current))
	}
	return out
}

func hasUpper(runes []rune) bool { return slices.ContainsFunc(runes, unicode.IsUpper) }

func hasDigit(runes []rune) bool { return slices.ContainsFunc(runes, unicode.IsDigit) }

// isUpper matches Python's str.isupper: every cased letter is upper and there
// is at least one.
func isUpper(runes []rune) bool {
	cased := false
	for _, r := range runes {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			cased = true
		}
	}
	return cased
}

func capitalize(runes []rune) string {
	if len(runes) == 0 {
		return ""
	}
	out := make([]rune, len(runes))
	out[0] = unicode.ToUpper(runes[0])
	for i, r := range runes[1:] {
		out[i+1] = unicode.ToLower(r)
	}
	return string(out)
}
