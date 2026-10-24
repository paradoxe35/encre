package witai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseResponseText(t *testing.T) {
	text, err := parseResponse([]byte(`{"text":"hello world"}`))
	if err != nil || text != "hello world" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestParseResponseLegacyText(t *testing.T) {
	text, err := parseResponse([]byte(`{"_text":"hello legacy"}`))
	if err != nil || text != "hello legacy" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestParseResponseError(t *testing.T) {
	_, err := parseResponse([]byte(`{"error":"Bad auth","code":"unauthorized"}`))
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseResponseNoText(t *testing.T) {
	text, err := parseResponse([]byte(`{"confidence":0.9}`))
	if err != nil || text != "" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestParseResponseInvalidJSON(t *testing.T) {
	if _, err := parseResponse([]byte(`not json`)); err == nil {
		t.Fatal("expected an error")
	}
}

func witServer(t *testing.T, handle func(chunk int, w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	withKeys(t, `[{"key":"K","lang":"en"}]`)

	var mu sync.Mutex
	chunk := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		chunk++
		n := chunk
		mu.Unlock()
		handle(n, w, r)
	}))
	t.Cleanup(server.Close)

	previous := endpoint
	endpoint = server.URL
	t.Cleanup(func() { endpoint = previous })
}

func chunksOfAudio(n int) []byte {
	return make([]byte, n*bytesPerChunk*captureSampleRate/witSampleRate)
}

func withChunkTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previousTimeout, previousBudget := chunkTimeout, chunkBudget
	chunkTimeout, chunkBudget = d, d
	t.Cleanup(func() { chunkTimeout, chunkBudget = previousTimeout, previousBudget })
}

func TestTranscribeKeepsTheOtherChunksAfterAFailure(t *testing.T) {
	witServer(t, func(chunk int, w http.ResponseWriter, r *http.Request) {
		if chunk == 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"text":"part %d"}`, chunk)
	})

	text, err := Transcribe(context.Background(), chunksOfAudio(4), "en")
	if err == nil {
		t.Fatal("expected the failed chunk's error")
	}
	if !strings.HasPrefix(text, "part 1 part 2 part 4") || strings.Contains(text, "part 3") {
		t.Fatalf("got %q, want every chunk but the failed one", text)
	}
}

// Four chunks outlast one chunk's deadline, so this fails if the deadline is shared.
func TestTranscribeTimesEachChunkSeparately(t *testing.T) {
	withChunkTimeout(t, 300*time.Millisecond)
	witServer(t, func(chunk int, w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintf(w, `{"text":"part %d"}`, chunk)
	})

	text, err := Transcribe(context.Background(), chunksOfAudio(4), "en")
	if err != nil || !strings.HasPrefix(text, "part 1 part 2 part 3 part 4") {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestTranscribeGivesUpOnAHungChunk(t *testing.T) {
	withChunkTimeout(t, 100*time.Millisecond)
	witServer(t, func(chunk int, w http.ResponseWriter, r *http.Request) {
		if chunk == 2 {
			io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		fmt.Fprintf(w, `{"text":"part %d"}`, chunk)
	})

	text, err := Transcribe(context.Background(), chunksOfAudio(2), "en")
	// A timeout means the line, not the chunk: the rest would only time out in turn.
	if !errors.Is(err, context.DeadlineExceeded) || text != "part 1" || !strings.Contains(err.Error(), "not sent") {
		t.Fatalf("got %q, %v; want the text before the hung chunk and the rest not sent", text, err)
	}
}

func TestTranscribeErrorIsShortForAnHTMLPage(t *testing.T) {
	witServer(t, func(chunk int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>"+strings.Repeat("x", 100_000)+"</html>")
	})

	_, err := Transcribe(context.Background(), chunksOfAudio(1), "en")
	if err == nil || len(err.Error()) > 500 {
		t.Fatalf("got an error of %d bytes", len(err.Error()))
	}
}
