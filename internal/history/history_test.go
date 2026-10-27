package history

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return &Store{path: filepath.Join(t.TempDir(), "history.jsonl")}
}

func TestAddAndRecentAreNewestFirst(t *testing.T) {
	store := testStore(t)

	first := Entry{Kind: KindRevise, Original: "a", Result: "b", At: time.Now().Add(-time.Minute)}
	second := Entry{Kind: KindDictate, Original: "c", Result: "d"}
	store.Add(first)
	store.Add(second)

	recent := store.Recent("")
	if len(recent) != 2 {
		t.Fatalf("got %d entries, want 2", len(recent))
	}
	if recent[0].Result != "d" || recent[1].Result != "b" {
		t.Errorf("newest-first order broken: %+v", recent)
	}
	if recent[0].ID == "" || recent[0].At.IsZero() {
		t.Error("Add must fill ID and timestamp")
	}
}

func TestRecentFiltersByKind(t *testing.T) {
	store := testStore(t)
	store.Add(Entry{Kind: KindRevise, Result: "r"})
	store.Add(Entry{Kind: KindTranslate, Result: "t", FromLang: "en", ToLang: "fr"})
	store.Add(Entry{Kind: KindDictate, Result: "s"})

	if got := len(store.Recent(KindTranslate)); got != 1 {
		t.Fatalf("translate filter returned %d entries, want 1", got)
	}
	if got := len(store.Recent("")); got != 3 {
		t.Fatalf("empty filter returned %d entries, want 3", got)
	}
}

func TestRecentFiltersBeforeApplyingTheEntryLimit(t *testing.T) {
	store := testStore(t)
	store.Add(Entry{Kind: KindAsk, Original: "question", Result: "answer"})
	for i := 0; i <= MaxEntries; i++ {
		store.Add(Entry{Kind: KindRevise, Result: "revision"})
	}

	asked := store.Recent(KindAsk)
	if len(asked) != 1 || asked[0].Original != "question" {
		t.Fatalf("Ask history after other actions = %+v, want the latest exchange", asked)
	}
}

func TestTrimKeepsNewestAtCap(t *testing.T) {
	store := testStore(t)
	for i := 0; i < MaxEntries+50; i++ {
		store.Add(Entry{Kind: KindRevise, Result: "x", At: time.Now().Add(time.Duration(i) * time.Second)})
	}

	entries := store.Recent("")
	if len(entries) != MaxEntries {
		t.Fatalf("kept %d entries, want %d", len(entries), MaxEntries)
	}

	// The oldest fifty must be gone: the first survivor is entry #50.
	if entries[len(entries)-1].Result == "0" {
		t.Error("the oldest entries should have been trimmed")
	}
}

func TestTrimKeepsRecentEntriesForEachKind(t *testing.T) {
	store := testStore(t)
	allKinds := []Kind{KindRevise, KindTranslate, KindDictate, KindAsk}
	for i := 0; i < maxFileEntries+1; i++ {
		kind := allKinds[i%len(allKinds)]
		store.Add(Entry{Kind: kind, Original: string(kind), Result: "result"})
	}

	asked := store.Recent(KindAsk)
	if len(asked) != MaxEntries {
		t.Fatalf("trim kept %d Ask entries, want %d", len(asked), MaxEntries)
	}
	if got := len(store.Recent(KindRevise)); got != MaxEntries {
		t.Fatalf("trim kept %d revise entries, want %d", got, MaxEntries)
	}
}

func TestClearRemovesEverything(t *testing.T) {
	store := testStore(t)
	store.Add(Entry{Kind: KindRevise, Result: "r"})
	if err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if got := len(store.Recent("")); got != 0 {
		t.Fatalf("after clear, %d entries remain", got)
	}
	if data, _ := os.ReadFile(store.path); len(data) != 0 {
		t.Errorf("clear left %q in the file", data)
	}
}

func TestClearOnAMissingFileIsNotAnError(t *testing.T) {
	store := testStore(t)
	if err := store.Clear(); err != nil {
		t.Fatalf("clearing an empty store should not error, got %v", err)
	}
}

func TestOnChangeCanReadTheStore(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "history.jsonl")}

	seen := make(chan int, 1)
	store.OnChange(func() {
		seen <- len(store.Recent(""))
	})

	done := make(chan struct{})
	go func() {
		store.Add(Entry{Kind: KindRevise, Original: "a", Result: "b"})
		close(done)
	}()

	select {
	case count := <-seen:
		if count != 1 {
			t.Errorf("handler saw %d entries, want 1", count)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange deadlocked reading the store it was notified about")
	}

	<-done
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestHistoryFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	store := testStore(t)
	store.Add(Entry{Kind: KindRevise, Result: "r"})
	if got := filePerm(t, store.path); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
}

func TestAnExistingWorldReadableHistoryIsTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	store := testStore(t)
	if err := os.WriteFile(store.path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	store.Add(Entry{Kind: KindRevise, Result: "r"})
	if got := filePerm(t, store.path); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
}

func TestClearingAnExistingWorldReadableHistoryKeepsItPrivateOnNextWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	store := testStore(t)
	if err := os.WriteFile(store.path, []byte(`{"kind":"ask","original":"secret"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	store.Add(Entry{Kind: KindAsk, Original: "new question", Result: "answer"})
	if got := filePerm(t, store.path); got != 0o600 {
		t.Errorf("mode = %o after clear and write, want 600", got)
	}
}

func TestTrimmedHistoryStaysPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	store := testStore(t)
	for i := 0; i < maxFileEntries+1; i++ {
		store.Add(Entry{Kind: KindRevise, Result: "x"})
	}
	if got := filePerm(t, store.path); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
}

func TestOneOverlongLineDoesNotHideTheRest(t *testing.T) {
	store := testStore(t)
	store.Add(Entry{Kind: KindRevise, Result: "before"})

	file, err := os.OpenFile(store.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(`{"kind":"revise","result":"` + strings.Repeat("x", 2<<20) + "\"}\n")
	file.WriteString("not json\n")
	file.Close()

	store.Add(Entry{Kind: KindRevise, Result: "after"})

	recent := store.Recent("")
	if len(recent) != 2 {
		t.Fatalf("got %d entries, want 2", len(recent))
	}
	if recent[0].Result != "after" || recent[1].Result != "before" {
		t.Errorf("unexpected entries: %+v", recent)
	}
}

func TestTrimIsRare(t *testing.T) {
	store := testStore(t)
	for i := 0; i < MaxEntries+10; i++ {
		store.Add(Entry{Kind: KindRevise, Result: "x"})
	}
	if got := lineCount(t, store.path); got != MaxEntries+10 {
		t.Fatalf("file has %d lines; it should not be rewritten just past the cap", got)
	}

	for i := MaxEntries + 10; i < maxFileEntries; i++ {
		store.Add(Entry{Kind: KindRevise, Result: "x"})
	}
	if got := lineCount(t, store.path); got != maxFileEntries {
		t.Fatalf("file has %d lines; it should not be rewritten at the cap", got)
	}

	store.Add(Entry{Kind: KindRevise, Result: "x"})
	if got := lineCount(t, store.path); got > MaxEntries {
		t.Fatalf("file has %d lines, should be trimmed to %d", got, MaxEntries)
	}
	if got := len(store.Recent("")); got != MaxEntries {
		t.Errorf("Recent returned %d, want %d", got, MaxEntries)
	}
}

func TestTrimCountsLinesWrittenBeforeTheStoreOpened(t *testing.T) {
	store := testStore(t)
	for i := 0; i < maxFileEntries; i++ {
		store.Add(Entry{Kind: KindRevise, Result: "x"})
	}

	reopened := &Store{path: store.path}
	reopened.Add(Entry{Kind: KindRevise, Result: "y"})
	if got := lineCount(t, store.path); got != MaxEntries {
		t.Errorf("file has %d lines after trimming, want %d", got, MaxEntries)
	}
}

func TestClearingALinkedHistoryEmptiesTheFileItPointsTo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "synced-history.jsonl")
	if err := os.WriteFile(target, []byte(`{"kind":"revise","original":"secret"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "history.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := (&Store{path: link}).Clear(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); len(data) != 0 {
		t.Fatalf("the linked file still holds %q", data)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("clearing removed the link")
	}
}
