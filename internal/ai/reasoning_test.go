package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func clearReasoningCache() {
	rejected.Range(func(key, _ any) bool {
		rejected.Delete(key)
		return true
	})
}

func recordingServer[T any](t *testing.T, replies ...http.HandlerFunc) (*httptest.Server, *[]T) {
	t.Helper()
	var seen []T
	attempt := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body T
		_ = json.Unmarshal(raw, &body)
		seen = append(seen, body)

		reply := replies[min(attempt, len(replies)-1)]
		attempt++
		reply(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

func reply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

var (
	chatOK     = reply(`{"choices":[{"message":{"role":"assistant","content":"corrigé"}}]}`)
	badRequest = status(http.StatusBadRequest, `{"error":{"message":"unsupported parameter"}}`)
)

func build(t *testing.T, name, url string, low bool) Provider {
	t.Helper()
	clearReasoningCache()
	p, err := FromSettings(name, config.ProviderSettings{BaseURL: url, Model: "test-model", LowReasoning: low}, "sk-test", false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func complete(t *testing.T, p Provider) (string, error) {
	t.Helper()
	return p.Stream(context.Background(), Prompt{System: "prompt", Text: "text"}, nil)
}

func TestLowReasoningSendsEffort(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, chatOK)

	if _, err := complete(t, build(t, config.BuiltInOpenAI, server.URL, true)); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 || (*seen)[0].ReasoningEffort != "low" {
		t.Fatalf("expected one request asking for low effort, got %+v", *seen)
	}
}

func TestReasoningIsNotSentWhenNotWanted(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, chatOK)

	if _, err := complete(t, build(t, config.BuiltInOpenAI, server.URL, false)); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 || (*seen)[0].ReasoningEffort != "" || (*seen)[0].Reasoning != nil {
		t.Fatalf("expected one plain request, got %+v", *seen)
	}
}

func TestARejectedParameterIsRetriedWithoutIt(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, badRequest, chatOK)

	result, err := complete(t, build(t, config.BuiltInOpenAI, server.URL, true))
	if err != nil {
		t.Fatal(err)
	}
	if result != "corrigé" {
		t.Fatalf("expected the retry's result, got %q", result)
	}
	if len(*seen) != 2 || (*seen)[0].ReasoningEffort != "low" || (*seen)[1].ReasoningEffort != "" {
		t.Fatalf("expected a request with the parameter then one without, got %+v", *seen)
	}
}

func TestARejectionIsRememberedForTheNextCall(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, badRequest, chatOK)
	p := build(t, config.BuiltInOpenAI, server.URL, true)

	for range 2 {
		if _, err := complete(t, p); err != nil {
			t.Fatal(err)
		}
	}
	if len(*seen) != 3 || (*seen)[2].ReasoningEffort != "" {
		t.Fatalf("expected the rejection to be remembered, got %+v", *seen)
	}
}

// Caching on a 400 alone would disable reasoning for a model that never objected.
func TestAPersistentBadRequestIsReportedAndNotCached(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, badRequest)
	if _, err := complete(t, build(t, config.BuiltInOpenAI, server.URL, true)); err == nil {
		t.Fatal("expected the error to surface")
	}
	if len(*seen) != 2 {
		t.Fatalf("expected one retry, got %d requests", len(*seen))
	}

	if _, refused := rejected.Load(server.URL + "::test-model"); refused {
		t.Fatal("an unrelated 400 disabled reasoning for later calls")
	}
}

func TestAServerErrorIsNotRetried(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, status(http.StatusInternalServerError, `{"error":{"message":"down"}}`))

	if _, err := complete(t, build(t, config.BuiltInOpenAI, server.URL, true)); err == nil {
		t.Fatal("expected the error to surface")
	}
	if len(*seen) != 1 {
		t.Fatalf("a server error should not be retried, got %d requests", len(*seen))
	}
}

func TestOpenRouterGetsItsOwnShape(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, chatOK)
	p := build(t, config.BuiltInOpenRouter, server.URL+"/openrouter.ai", true)

	if _, err := complete(t, p); err != nil {
		t.Fatal(err)
	}
	request := (*seen)[0]
	if request.ReasoningEffort != "" || request.Reasoning == nil || !request.Reasoning.Exclude {
		t.Fatalf("expected OpenRouter's reasoning object alone, got %+v", request)
	}
}

func TestClaudeIsAskedForLowEffortAndAnOlderModelIsAskedAgainWithout(t *testing.T) {
	server, seen := recordingServer[messagesRequest](t, badRequest, reply(`{"content":[{"type":"text","text":"ok"}]}`))

	if _, err := complete(t, build(t, config.BuiltInClaude, server.URL, true)); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 2 {
		t.Fatalf("sent %d requests, want the refused effort retried once", len(*seen))
	}
	first, retry := (*seen)[0], (*seen)[1]
	if first.OutputConfig == nil || first.OutputConfig.Effort != "low" || retry.OutputConfig != nil {
		t.Fatalf("sent %+v then %+v, want low effort then none", first.OutputConfig, retry.OutputConfig)
	}
}

func TestGeminiThinksLessByModelGeneration(t *testing.T) {
	server, seen := recordingServer[geminiRequest](t, reply(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))

	for _, model := range []string{"gemini-2.5-flash", "gemini-3.1-flash-lite"} {
		clearReasoningCache()
		p, _ := FromSettings(config.BuiltInGemini, config.ProviderSettings{BaseURL: server.URL, Model: model, LowReasoning: true}, "k", false)
		if _, err := complete(t, p); err != nil {
			t.Fatal(err)
		}
	}

	older, newer := (*seen)[0].GenerationConfig.ThinkingConfig, (*seen)[1].GenerationConfig.ThinkingConfig
	if older == nil || older.ThinkingBudget == nil || *older.ThinkingBudget != 0 || older.ThinkingLevel != "" {
		t.Fatalf("gemini 2 should get a zero budget, got %+v", older)
	}
	if newer == nil || newer.ThinkingLevel != "low" || newer.ThinkingBudget != nil {
		t.Fatalf("gemini 3 should get a low level, got %+v", newer)
	}
}
