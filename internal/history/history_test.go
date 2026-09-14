package history

import (
	"os"
	"path/filepath"
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
	second := Entry{Kind: KindSpeech, Original: "c", Result: "d"}
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
	store.Add(Entry{Kind: KindSpeech, Result: "s"})

	if got := len(store.Recent(KindTranslate)); got != 1 {
		t.Fatalf("translate filter returned %d entries, want 1", got)
	}
	if got := len(store.Recent("")); got != 3 {
		t.Fatalf("empty filter returned %d entries, want 3", got)
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

func TestClearRemovesEverything(t *testing.T) {
	store := testStore(t)
	store.Add(Entry{Kind: KindRevise, Result: "r"})
	if err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if got := len(store.Recent("")); got != 0 {
		t.Fatalf("after clear, %d entries remain", got)
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Error("clear should remove the file")
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
