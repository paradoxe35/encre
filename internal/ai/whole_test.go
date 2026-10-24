package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

// refusesToStream answers a streamed request as OpenAI does an unverified organisation, and any other with whole.
func refusesToStream(whole string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"stream":true`) || strings.Contains(r.URL.Path, "streamGenerateContent") {
			http.Error(w, `{"error":{"message":"Your organization must be verified to stream this model."}}`, http.StatusBadRequest)
			return
		}
		io.WriteString(w, whole)
	}
}

func TestEachProtocolReadsAToolCallFromAWholeReply(t *testing.T) {
	cases := []struct {
		provider string
		whole    string
		text     string
	}{
		{config.BuiltInOpenAI, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"place\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`, ""},
		{config.BuiltInClaude, `{"content":[{"type":"thinking","thinking":"A lookup.","signature":"sig"},{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"Let me check."},{"type":"tool_use","id":"call_1","name":"get_weather","input":{"place":"Paris"}}],"stop_reason":"tool_use"}`, "Let me check."},
		{config.BuiltInGemini, `{"candidates":[{"content":{"parts":[{"text":"musing","thought":true},{"functionCall":{"id":"call_1","name":"get_weather","args":{"place":"Paris"}},"thoughtSignature":"sig"}]},"finishReason":"STOP"}]}`, ""},
	}

	for _, c := range cases {
		server := httptest.NewServer(refusesToStream(c.whole))
		p, _ := FromSettings(c.provider, config.ProviderSettings{BaseURL: server.URL}, "k", false)

		reply, err := p.(ToolUser).Turn(context.Background(), Prompt{Text: "Weather?", Tools: []Tool{weatherTool}}, func(string) {})
		server.Close()
		if err != nil {
			t.Fatalf("%s: %v", c.provider, err)
		}
		if len(reply.Calls) != 1 || reply.Text != c.text {
			t.Fatalf("%s: got %d calls and %q, want 1 and %q", c.provider, len(reply.Calls), reply.Text, c.text)
		}
		call := reply.Calls[0]
		var args struct{ Place string }
		if err := json.Unmarshal(call.Arguments, &args); err != nil || call.ID != "call_1" || call.Name != "get_weather" || args.Place != "Paris" {
			t.Errorf("%s: read the call as %+v (%s)", c.provider, call, call.Arguments)
		}
		switch c.provider {
		case config.BuiltInClaude:
			want := []anthropicThinking{{Type: "thinking", Thinking: "A lookup.", Signature: "sig"}, {Type: "redacted_thinking", Data: "opaque"}}
			if len(call.thinking) != 2 || call.thinking[0] != want[0] || call.thinking[1] != want[1] {
				t.Errorf("kept the thinking as %+v, want %+v", call.thinking, want)
			}
		case config.BuiltInGemini:
			if call.signature != "sig" {
				t.Errorf("kept the signature as %q", call.signature)
			}
		}
	}
}

func TestAWholeReplyStopsAsAStreamedOneWould(t *testing.T) {
	cases := []struct {
		provider string
		whole    string
		want     error
	}{
		{config.BuiltInOpenAI, `{"choices":[{"message":{"content":null},"finish_reason":"stop"}]}`, errNoReply},
		{config.BuiltInOpenAI, `{"choices":[{"message":{"tool_calls":[{"id":"call_1","function":{"name":"get_weather","arguments":"{"}}]},"finish_reason":"length"}]}`, errLengthLimit},
		{config.BuiltInClaude, `{"content":[{"type":"tool_use","id":"call_1","name":"get_weather","input":{}}],"stop_reason":"max_tokens"}`, errLengthLimit},
		{config.BuiltInClaude, `{"content":[{"type":"text","text":"No."}],"stop_reason":"refusal"}`, errRefused},
		{config.BuiltInClaude, `{"content":[{"type":"thinking","thinking":"hm","signature":"sig"}],"stop_reason":"end_turn"}`, errNoReply},
		{config.BuiltInGemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather"}}]},"finishReason":"MAX_TOKENS"}]}`, errLengthLimit},
		{config.BuiltInGemini, `{"candidates":[{"content":{"parts":[{"text":"musing","thought":true}]},"finishReason":"STOP"}]}`, errNoReply},
	}
	for _, c := range cases {
		server := httptest.NewServer(refusesToStream(c.whole))
		p, _ := FromSettings(c.provider, config.ProviderSettings{BaseURL: server.URL}, "k", false)

		var shown string
		reply, err := p.(ToolUser).Turn(context.Background(), Prompt{Text: "Weather?", Tools: []Tool{weatherTool}}, func(text string) { shown += text })
		server.Close()
		if err == nil || !strings.Contains(err.Error(), c.want.Error()) || len(reply.Calls) != 0 || reply.Text != "" || shown != "" {
			t.Errorf("%s %s: got %+v, shown %q, %v; want %v", c.provider, c.whole, reply, shown, err, c.want)
		}
	}
}
