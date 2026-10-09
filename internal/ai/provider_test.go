package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/config"
)

func sse(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			w.Write([]byte("data: " + event + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}
}

func stream(t *testing.T, p Provider) ([]string, string, error) {
	t.Helper()
	var pieces []string
	reply, err := p.Stream(context.Background(), Prompt{System: "s", Text: "t"}, func(text string) {
		pieces = append(pieces, text)
	})
	return pieces, reply, err
}

func TestEachProtocolStreams(t *testing.T) {
	cases := []struct {
		provider string
		events   []string
	}{
		{config.BuiltInOpenAI, []string{
			`{"choices":[{"delta":{"role":"assistant","content":""}}]}`,
			`{"choices":[{"delta":{"content":"Bon"}}]}`,
			`{"choices":[{"delta":{"content":"jour"}}]}`,
			`{"choices":[]}`,
			`[DONE]`,
		}},
		{config.BuiltInClaude, []string{
			`{"type":"message_start","message":{}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Bon"}}`,
			`{"type":"ping"}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"jour"}}`,
			`{"type":"message_stop"}`,
		}},
		{config.BuiltInGemini, []string{
			`{"candidates":[{"content":{"parts":[{"text":"thinking","thought":true},{"text":"Bon"}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"jour"}]}}]}`,
		}},
	}

	for _, c := range cases {
		server := httptest.NewServer(sse(c.events...))
		p, _ := FromSettings(c.provider, config.ProviderSettings{BaseURL: server.URL}, "k", false)

		pieces, reply, err := stream(t, p)
		server.Close()
		if err != nil {
			t.Fatalf("%s: %v", c.provider, err)
		}
		if reply != "Bonjour" || strings.Join(pieces, "|") != "Bon|jour" {
			t.Errorf("%s: streamed %q as %v", c.provider, reply, pieces)
		}
	}
}

func TestEveryEventOfAStreamCountsAsActivityButAnError(t *testing.T) {
	server := httptest.NewServer(sse(
		`{"choices":[{"delta":{"content":"","reasoning":"Thinking"}}]}`,
		`{"choices":[{"delta":{"content":"","reasoning":" hard"}}]}`,
		`{"choices":[{"delta":{"content":"Bon"}}]}`,
		`{"error":{"message":"upstream overloaded"}}`,
	))
	defer server.Close()
	p, _ := FromSettings(config.BuiltInOpenRouter, config.ProviderSettings{BaseURL: server.URL}, "k", false)

	events := 0
	ctx := WithActivity(context.Background(), func() { events++ })
	if _, err := p.Stream(ctx, Prompt{Text: "t"}, nil); err == nil {
		t.Fatal("the error event went unreported")
	}
	if events != 3 {
		t.Fatalf("counted %d events, want the 3 before the error", events)
	}
}

func TestAnErrorMidStreamKeepsWhatArrived(t *testing.T) {
	server := httptest.NewServer(sse(
		`{"choices":[{"delta":{"content":"Bon"}}]}`,
		`{"error":{"message":"upstream overloaded"}}`,
	))
	defer server.Close()
	p, _ := FromSettings(config.BuiltInOpenRouter, config.ProviderSettings{BaseURL: server.URL}, "k", false)

	_, reply, err := stream(t, p)
	if err == nil || !strings.Contains(err.Error(), "upstream overloaded") {
		t.Fatalf("got %v", err)
	}
	if reply != "Bon" {
		t.Fatalf("kept %q", reply)
	}
}

func TestCancellingEndsAStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Bon\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	p, _ := FromSettings(config.BuiltInOpenAI, config.ProviderSettings{BaseURL: server.URL}, "k", false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Stream(ctx, Prompt{Text: "t"}, func(string) { cancel() })
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want a cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream outlived its context")
	}
}

func TestARedirectIsReportedNotFollowed(t *testing.T) {
	target := httptest.NewServer(chatOK)
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer redirect.Close()

	p, _ := FromSettings("ollama", config.ProviderSettings{BaseURL: redirect.URL + "/v1"}, "", true)
	_, err := complete(t, p)
	if err == nil || !strings.Contains(err.Error(), "redirects to "+target.URL) {
		t.Fatalf("got %v", err)
	}
}

func TestErrorsReadBothShapes(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"model 'x' not found": status(http.StatusNotFound, `{"error":"model 'x' not found"}`),
		"invalid key":         status(http.StatusUnauthorized, `{"error":{"message":"invalid key"}}`),
		"check the base URL":  status(http.StatusMethodNotAllowed, `405 method not allowed`),
	}
	for want, handler := range cases {
		server := httptest.NewServer(handler)
		p, _ := FromSettings("ollama", config.ProviderSettings{BaseURL: server.URL + "/v1"}, "", true)
		_, err := complete(t, p)
		server.Close()

		var apiErr *APIError
		if !errors.As(err, &apiErr) || !strings.Contains(err.Error(), want) || !strings.HasPrefix(err.Error(), "ollama: ") {
			t.Errorf("got %v, want an API error mentioning %q", err, want)
		}
	}
}

func TestFromSettings(t *testing.T) {
	p, err := FromSettings(config.BuiltInOpenRouter, config.ProviderSettings{}, "k", false)
	if err != nil || p.Name() != config.BuiltInOpenRouter || p.Model() != "openai/gpt-6-luna" {
		t.Fatalf("built-in defaults: %v %v", p, err)
	}

	for in, want := range map[string]string{
		"http://localhost:11434":      "http://localhost:11434/v1",
		"http://localhost:11434/":     "http://localhost:11434/v1",
		"https://host.example/v1/":    "https://host.example/v1",
		"https://api.groq.com/openai": "https://api.groq.com/openai",
	} {
		p, err := FromSettings("local", config.ProviderSettings{BaseURL: in, Model: "llama3"}, "", true)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.(*provider).endpoint.baseURL; got != want {
			t.Errorf("%s became %s, want %s", in, got, want)
		}
	}

	if _, err := FromSettings("local", config.ProviderSettings{}, "", true); err == nil {
		t.Error("a custom provider without a base URL was accepted")
	}
	if _, err := FromSettings("local", config.ProviderSettings{BaseURL: "http://x", ProviderType: "grpc"}, "", true); err == nil {
		t.Error("an unsupported custom type was accepted")
	}
	if _, err := FromSettings("unknown", config.ProviderSettings{}, "", false); err == nil {
		t.Error("an unknown built-in was accepted")
	}
}

func TestReadEventsJoinsMultilineData(t *testing.T) {
	body := ": comment\nevent: x\ndata: one\ndata: two\n\ndata: three\n"
	var got []string
	err := readEvents(strings.NewReader(body), func(data []byte) (bool, error) {
		got = append(got, string(data))
		return false, nil
	})
	if err != nil || strings.Join(got, "|") != "one\ntwo|three" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAReplyCutOffByTheLengthLimitSaysSo(t *testing.T) {
	cases := []struct {
		provider string
		events   []string
	}{
		{config.BuiltInOpenAI, []string{
			`{"choices":[{"delta":{"content":"Bon"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"length"}]}`,
			`[DONE]`,
		}},
		{config.BuiltInClaude, []string{
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Bon"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`,
			`{"type":"message_stop"}`,
		}},
		{config.BuiltInGemini, []string{
			`{"candidates":[{"content":{"parts":[{"text":"Bon"}]},"finishReason":"MAX_TOKENS"}]}`,
		}},
	}

	for _, c := range cases {
		server := httptest.NewServer(sse(c.events...))
		p, _ := FromSettings(c.provider, config.ProviderSettings{BaseURL: server.URL}, "k", false)
		_, reply, err := stream(t, p)
		server.Close()

		if !errors.Is(err, errLengthLimit) || reply != "Bon" {
			t.Errorf("%s: kept %q with %v", c.provider, reply, err)
		}
	}
}

// A truncated rewrite pasted over the user's text would lose the rest of it.
func TestACompleteReplyCutOffIsNotReturned(t *testing.T) {
	server := httptest.NewServer(reply(`{"choices":[{"message":{"content":"Bon"},"finish_reason":"length"}]}`))
	defer server.Close()
	p, _ := FromSettings(config.BuiltInOpenAI, config.ProviderSettings{BaseURL: server.URL}, "k", false)

	text, err := complete(t, p)
	if err == nil || !strings.Contains(err.Error(), "length limit") || text != "" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestAWholeReplyIsReadWhenTheServerIgnoresTheStream(t *testing.T) {
	cases := map[string]string{
		config.BuiltInOpenAI: `{"choices":[{"message":{"content":"Bonjour"}}]}`,
		config.BuiltInClaude: `{"content":[{"type":"text","text":"Bonjour"}]}`,
		config.BuiltInGemini: `{"candidates":[{"content":{"parts":[{"text":"Bonjour"}]}}]}`,
	}
	for provider, body := range cases {
		server := httptest.NewServer(reply(body))
		p, _ := FromSettings(provider, config.ProviderSettings{BaseURL: server.URL}, "k", false)

		pieces, text, err := stream(t, p)
		server.Close()
		if err != nil || text != "Bonjour" || strings.Join(pieces, "|") != "Bonjour" {
			t.Errorf("%s: got %q as %v, %v", provider, text, pieces, err)
		}
	}
}

// Revise pastes what comes back, so a reply that is not one must be an error.
func TestSomethingOtherThanAReplyIsReportedNotReturned(t *testing.T) {
	cases := map[string]string{
		"<html><body>Welcome</body></html>": "web page",
		"":                                  "empty reply",
		`{"status":"ok"}`:                   "unexpected reply",
	}
	for body, want := range cases {
		server := httptest.NewServer(reply(body))
		p, _ := FromSettings(config.BuiltInOpenAI, config.ProviderSettings{BaseURL: server.URL}, "k", false)

		text, err := complete(t, p)
		server.Close()
		if err == nil || !strings.Contains(err.Error(), want) || text != "" {
			t.Errorf("%q: got %q, %v; want an error saying %q", body, text, err, want)
		}
	}
}

func TestAServerThatRefusesToStreamIsAskedForTheWholeReply(t *testing.T) {
	refusal := status(http.StatusBadRequest, `{"error":{"message":"Unsupported value: 'stream' does not support true with this model."}}`)
	server, seen := recordingServer[chatRequest](t, refusal, chatOK, chatOK)
	p, _ := FromSettings("local", config.ProviderSettings{BaseURL: server.URL + "/v1", Model: "no-stream"}, "", true)

	for range 2 {
		if text, err := complete(t, p); err != nil || text != "corrigé" {
			t.Fatalf("got %q, %v", text, err)
		}
	}
	if len(*seen) != 3 || !(*seen)[0].Stream || (*seen)[1].Stream || (*seen)[2].Stream {
		t.Fatalf("streamed %d requests as %+v, want one refused stream then whole replies", len(*seen), *seen)
	}
}

func TestOtherRefusalsAreNotRetriedWithoutStreaming(t *testing.T) {
	server, seen := recordingServer[chatRequest](t, status(http.StatusBadRequest, `{"error":{"message":"model not found"}}`))
	p, _ := FromSettings("local", config.ProviderSettings{BaseURL: server.URL + "/v1", Model: "missing"}, "", true)

	if _, err := complete(t, p); err == nil || len(*seen) != 1 {
		t.Fatalf("got %v after %d requests, want the refusal reported as it came", err, len(*seen))
	}
}
