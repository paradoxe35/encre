package stt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func payload(size int) []byte {
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i % 251)
	}
	return body
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// serve honours Range so resume can be exercised; ignoreRange reproduces a server that always answers 200.
func serve(t *testing.T, body []byte, ignoreRange bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if spec := r.Header.Get("Range"); spec != "" && !ignoreRange {
			fmt.Sscanf(spec, "bytes=%d-", &start)
			if start >= len(body) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range",
				fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(body[start:])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}))
}

func testStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore()
	store.dir = t.TempDir()
	return store
}

func testModel(server *httptest.Server, body []byte) Model {
	return Model{
		ID:        "test/model",
		Name:      "Test Model",
		Repo:      strings.TrimPrefix(server.URL, "http://"),
		Filename:  "model.gguf",
		SizeBytes: int64(len(body)),
		SHA256:    digest(body),
	}
}

func TestDownloadVerifiesAndStores(t *testing.T) {
	body := payload(64 * 1024)
	server := serve(t, body, false)
	defer server.Close()

	store := testStore(t)
	model := testModel(server, body)
	store.urlFor = func(Model) string { return server.URL }

	if err := store.Download(context.Background(), model, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}

	if !store.Downloaded(model) {
		t.Fatal("model should be reported as downloaded")
	}

	written, err := os.ReadFile(store.Path(model))
	if err != nil {
		t.Fatal(err)
	}
	if digest(written) != model.SHA256 {
		t.Fatal("stored file does not match the published checksum")
	}
	if _, err := os.Stat(store.Path(model) + partialSuffix); !os.IsNotExist(err) {
		t.Fatal("partial file should be gone after a successful download")
	}
}

func TestDownloadResumesFromPartial(t *testing.T) {
	body := payload(64 * 1024)
	server := serve(t, body, false)
	defer server.Close()

	store := testStore(t)
	model := testModel(server, body)
	store.urlFor = func(Model) string { return server.URL }

	half := len(body) / 2
	partial := store.Path(model) + partialSuffix
	if err := os.WriteFile(partial, body[:half], 0o644); err != nil {
		t.Fatal(err)
	}

	var firstReport int64 = -1
	err := store.Download(context.Background(), model, func(p Progress) {
		if firstReport < 0 && p.Stage == StageDownloading {
			firstReport = p.Downloaded
		}
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if firstReport >= 0 && firstReport < int64(half) {
		t.Errorf("resumed from %d, expected to continue past %d", firstReport, half)
	}

	written, _ := os.ReadFile(store.Path(model))
	if digest(written) != model.SHA256 {
		t.Fatal("resumed file is corrupt")
	}
}

// A server that ignores Range replies 200 from byte zero; appending would duplicate the prefix.
func TestDownloadHandlesIgnoredRange(t *testing.T) {
	body := payload(32 * 1024)
	server := serve(t, body, true)
	defer server.Close()

	store := testStore(t)
	model := testModel(server, body)
	store.urlFor = func(Model) string { return server.URL }

	partial := store.Path(model) + partialSuffix
	if err := os.WriteFile(partial, body[:1024], 0o644); err != nil {
		t.Fatal(err)
	}

	if err := store.Download(context.Background(), model, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}

	written, _ := os.ReadFile(store.Path(model))
	if len(written) != len(body) {
		t.Fatalf("got %d bytes, want %d", len(written), len(body))
	}
	if digest(written) != model.SHA256 {
		t.Fatal("file corrupted by an ignored Range header")
	}
}

func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	body := payload(8 * 1024)
	server := serve(t, body, false)
	defer server.Close()

	store := testStore(t)
	model := testModel(server, body)
	model.SHA256 = digest(payload(16))
	store.urlFor = func(Model) string { return server.URL }

	err := store.Download(context.Background(), model, nil)
	if err == nil {
		t.Fatal("a mismatched checksum must fail")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("unexpected error: %v", err)
	}

	// The corrupt partial must be gone, or the next attempt resumes it forever.
	if _, err := os.Stat(store.Path(model) + partialSuffix); !os.IsNotExist(err) {
		t.Error("a corrupt partial should be deleted, not left to resume")
	}
	if store.Downloaded(model) {
		t.Error("a failed download must not be reported as present")
	}
}

// A full-size partial needs verifying, not another request: asking from EOF returns 416 and loops.
func TestDownloadVerifiesCompletePartial(t *testing.T) {
	body := payload(4096)
	server := serve(t, body, false)
	defer server.Close()

	store := testStore(t)
	model := testModel(server, body)
	store.urlFor = func(Model) string { return server.URL }

	if err := os.WriteFile(store.Path(model)+partialSuffix, body, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := store.Download(context.Background(), model, nil); err != nil {
		t.Fatalf("a complete partial should verify and move: %v", err)
	}
	if !store.Downloaded(model) {
		t.Fatal("model should be present")
	}
}

func TestDownloadStopsAtDeclaredSize(t *testing.T) {
	body := payload(8192)
	// The server sends more than it promised.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(append(body, payload(4096)...))
	}))
	defer server.Close()

	store := testStore(t)
	model := testModel(server, body)
	store.urlFor = func(Model) string { return server.URL }

	if err := store.Download(context.Background(), model, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}

	info, err := os.Stat(store.Path(model))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != model.SizeBytes {
		t.Fatalf("wrote %d bytes, expected to stop at %d", info.Size(), model.SizeBytes)
	}
}

func TestDeleteRemovesFileAndPartial(t *testing.T) {
	store := testStore(t)
	model := Model{ID: "a/b", Filename: "m.gguf", SizeBytes: 4}

	path := store.Path(model)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("abcd"), 0o644)
	os.WriteFile(path+partialSuffix, []byte("ab"), 0o644)

	if err := store.Delete(model); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("model file survived delete")
	}
	if _, err := os.Stat(path + partialSuffix); !os.IsNotExist(err) {
		t.Error("partial survived delete")
	}
}

func TestDownloadedRequiresFullSize(t *testing.T) {
	store := testStore(t)
	model := Model{ID: "a/b", Filename: "m.gguf", SizeBytes: 100}

	os.WriteFile(store.Path(model), payload(50), 0o644)
	if store.Downloaded(model) {
		t.Error("a short file must not count as downloaded")
	}

	os.WriteFile(store.Path(model), payload(100), 0o644)
	if !store.Downloaded(model) {
		t.Error("a full-size file should count as downloaded")
	}
}

func TestADeadConnectionIsCutByTheWatchdog(t *testing.T) {
	body := payload(4096)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(http.StatusOK)
		w.Write(body[:1024])
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(func() { close(release); server.Close() })

	store := testStore(t)
	store.stall = 100 * time.Millisecond
	store.urlFor = func(Model) string { return server.URL }
	model := testModel(server, body)

	done := make(chan error, 1)
	go func() { done <- store.Download(context.Background(), model, func(Progress) {}) }()

	select {
	case err := <-done:
		if !errors.Is(err, errStalled) {
			t.Fatalf("download ended with %v, want the stall error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled download never returned")
	}
}
