package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// geminiServer records whether each request carried a thinking budget.
func geminiServer(t *testing.T, replies ...func(w http.ResponseWriter)) (*httptest.Server, *[]GeminiRequest) {
	t.Helper()
	var seen []GeminiRequest
	attempt := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body GeminiRequest
		_ = json.Unmarshal(raw, &body)
		seen = append(seen, body)

		reply := replies[len(replies)-1]
		if attempt < len(replies) {
			reply = replies[attempt]
		}
		attempt++
		reply(w)
	}))
	t.Cleanup(server.Close)

	return server, &seen
}

func geminiOK(w http.ResponseWriter) {
	io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"corrigé"}]}}]}`)
}

func geminiProvider(t *testing.T, url string, low bool) *GeminiProvider {
	t.Helper()
	clearReasoningCache()
	p := NewGeminiProvider("k", url, "gemini-test", 1.0)
	p.LowReasoning = low
	return p
}

func TestGeminiAsksForNoThinkingWhenLowReasoningIsOn(t *testing.T) {
	server, seen := geminiServer(t, geminiOK)

	if _, err := geminiProvider(t, server.URL, true).
		ReviseText(context.Background(), "text", "prompt"); err != nil {
		t.Fatalf("revise: %v", err)
	}

	if len(*seen) != 1 || (*seen)[0].GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("expected a thinking budget, got %+v", *seen)
	}
}

func TestGeminiDoesNotAskForNoThinkingWhenLowReasoningIsOff(t *testing.T) {
	server, seen := geminiServer(t, geminiOK)

	if _, err := geminiProvider(t, server.URL, false).
		ReviseText(context.Background(), "text", "prompt"); err != nil {
		t.Fatalf("revise: %v", err)
	}

	if len(*seen) != 1 || (*seen)[0].GenerationConfig.ThinkingConfig != nil {
		t.Fatalf("expected no thinking budget, got %+v", *seen)
	}
}

// A model that cannot switch thinking off rejects a zero budget outright; the request must retry
// without it.
func TestGeminiRetriesWithoutTheBudgetWhenRefused(t *testing.T) {
	server, seen := geminiServer(t, badRequest, geminiOK)

	result, err := geminiProvider(t, server.URL, true).
		ReviseText(context.Background(), "text", "prompt")
	if err != nil {
		t.Fatalf("revise: %v", err)
	}

	if result != "corrigé" {
		t.Fatalf("expected the retry's result, got %q", result)
	}
	if len(*seen) != 2 {
		t.Fatalf("expected one retry, got %d requests", len(*seen))
	}
	if (*seen)[0].GenerationConfig.ThinkingConfig == nil {
		t.Fatal("the first attempt should carry the budget")
	}
	if (*seen)[1].GenerationConfig.ThinkingConfig != nil {
		t.Fatal("the retry should drop the budget")
	}
}
