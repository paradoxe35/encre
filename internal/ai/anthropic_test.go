package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

var claudeOK = sse(
	`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
	`{"type":"message_stop"}`,
)

func claude(t *testing.T, url, model string, temperature float64) Provider {
	t.Helper()
	p, err := FromSettings(config.BuiltInClaude, config.ProviderSettings{BaseURL: url, Model: model, Temperature: temperature}, "k", false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Current Claude models refuse temperature outright, so the default is left unsaid.
func TestClaudeIsSentATemperatureOnlyWhenOneIsSet(t *testing.T) {
	for temperature, want := range map[float64]any{0: nil, 1: nil, 0.3: 0.3} {
		server, seen := recordingServer[map[string]any](t, claudeOK)
		if _, err := complete(t, claude(t, server.URL, "claude-test", temperature)); err != nil {
			t.Fatal(err)
		}
		if got := (*seen)[0]["temperature"]; got != want {
			t.Errorf("temperature %v was sent as %v, want %v", temperature, got, want)
		}
	}
}

func TestARefusedTemperatureIsDroppedAndRemembered(t *testing.T) {
	refusal := status(http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"temperature is deprecated for this model."}}`)
	server, seen := recordingServer[map[string]any](t, refusal, claudeOK)
	p := claude(t, server.URL, "claude-no-temperature", 0.3)

	for range 2 {
		if text, err := complete(t, p); err != nil || text != "ok" {
			t.Fatalf("got %q, %v", text, err)
		}
	}
	if len(*seen) != 3 {
		t.Fatalf("sent %d requests, want the refusal retried once and remembered", len(*seen))
	}
	if _, sent := (*seen)[0]["temperature"]; !sent {
		t.Fatal("the set temperature was never tried")
	}
	for _, request := range (*seen)[1:] {
		if _, sent := request["temperature"]; sent {
			t.Fatalf("the refused temperature was sent again: %v", request)
		}
	}
}

// Thinking is on by default and shares the allowance with the text.
func TestClaudeIsGivenRoomToThink(t *testing.T) {
	server, seen := recordingServer[messagesRequest](t, claudeOK)
	if _, err := complete(t, claude(t, server.URL, "claude-test", 0)); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0].MaxTokens; got != 64000 {
		t.Fatalf("max_tokens = %d, want 64000", got)
	}
}

func TestAModelWithASmallerOutputLimitIsAskedAgainWithinIt(t *testing.T) {
	refusal := status(http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 64000 > 32000, which is the maximum allowed number of output tokens for claude-opus-4-1"}}`)
	server, seen := recordingServer[messagesRequest](t, refusal, claudeOK)
	p := claude(t, server.URL, "claude-smaller-limit", 0)

	for range 2 {
		if _, err := complete(t, p); err != nil {
			t.Fatal(err)
		}
	}
	if len(*seen) != 3 || (*seen)[1].MaxTokens != 32000 || (*seen)[2].MaxTokens != 32000 {
		t.Fatalf("sent %d requests, want 64000 then 32000 twice: %+v", len(*seen), *seen)
	}
}

// Claude refuses a tool result whose turn lost the thinking that led to the call.
func TestClaudeThinkingIsSentBackBeforeItsToolCall(t *testing.T) {
	server := httptest.NewServer(sse(
		`{"type":"message_start","message":{}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Paris "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weather."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_1","name":"get_weather","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"place\":\"Paris\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`,
	))
	defer server.Close()
	p := claude(t, server.URL, "claude-test", 0)

	prompt := Prompt{Text: "Weather?", Tools: []Tool{weatherTool}}
	reply, err := p.(ToolUser).Turn(context.Background(), prompt, nil)
	if err != nil || reply.Text != "" || len(reply.Calls) != 1 {
		t.Fatalf("got %+v, %v", reply, err)
	}

	prompt.Steps = []Step{{Calls: reply.Calls, Results: []string{"Sunny"}}}
	request, err := messages{}.request(context.Background(), endpoint{baseURL: "http://example.test", model: "m"}, prompt, true, false)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(request.Body)
	want := `{"role":"assistant","content":[` +
		`{"type":"thinking","thinking":"Paris weather.","signature":"sig-1"},` +
		`{"type":"redacted_thinking","data":"opaque"},` +
		`{"type":"tool_use","id":"call_1","name":"get_weather","input":{"place":"Paris"}}]}`
	if !strings.Contains(string(body), want) {
		t.Fatalf("request lacks %s\n%s", want, body)
	}
}
