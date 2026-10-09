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

var weatherTool = Tool{
	Name:        "get_weather",
	Description: "Weather for a place",
	Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"place": map[string]any{"type": "string"}},
	},
}

func TestEachProtocolStreamsAToolCall(t *testing.T) {
	cases := []struct {
		provider string
		events   []string
	}{
		{config.BuiltInOpenAI, []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"place\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		}},
		{config.BuiltInClaude, []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me check."}}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"get_weather","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"place\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}`,
			`{"type":"message_stop"}`,
		}},
		{config.BuiltInGemini, []string{
			`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_1","name":"get_weather","args":{"place":"Paris"}},"thoughtSignature":"sig"}]}}]}`,
		}},
	}

	for _, c := range cases {
		server := httptest.NewServer(sse(c.events...))
		p, _ := FromSettings(c.provider, config.ProviderSettings{BaseURL: server.URL}, "k", false)

		reply, err := p.(ToolUser).Turn(context.Background(), Prompt{Text: "Weather?", Tools: []Tool{weatherTool}}, func(string) {})
		server.Close()
		if err != nil {
			t.Fatalf("%s: %v", c.provider, err)
		}
		if len(reply.Calls) != 1 {
			t.Fatalf("%s: got %d calls, want 1", c.provider, len(reply.Calls))
		}
		call := reply.Calls[0]
		var args struct{ Place string }
		if err := json.Unmarshal(call.Arguments, &args); err != nil || call.ID != "call_1" || call.Name != "get_weather" || args.Place != "Paris" {
			t.Errorf("%s: read the call as %+v (%s)", c.provider, call, call.Arguments)
		}
	}
}

func TestEachProtocolSendsToolsAndTheirResults(t *testing.T) {
	prompt := Prompt{
		System: "s",
		Text:   "Weather in Paris?",
		Tools:  []Tool{weatherTool},
		Steps: []Step{{
			Calls:   []ToolCall{{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"place":"Paris"}`), signature: "sig"}},
			Results: []string{"Sunny, 21°C"},
		}},
	}
	cases := []struct {
		name     string
		protocol protocol
		want     []string
	}{
		{"openai", chatCompletions{}, []string{`"tools":[{"type":"function","function":{"name":"get_weather"`, `"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"place\":\"Paris\"}"}}]`, `{"role":"tool","content":"Sunny, 21°C","tool_call_id":"call_1"}`}},
		{"anthropic", messages{}, []string{`"tools":[{"name":"get_weather","description":"Weather for a place","input_schema"`, `{"type":"tool_use","id":"call_1","name":"get_weather","input":{"place":"Paris"}}`, `{"type":"tool_result","tool_use_id":"call_1","content":"Sunny, 21°C"}`}},
		{"gemini", generateContent{}, []string{`"functionDeclarations":[{"name":"get_weather"`, `{"functionCall":{"id":"call_1","name":"get_weather","args":{"place":"Paris"}},"thoughtSignature":"sig"}`, `{"functionResponse":{"id":"call_1","name":"get_weather","response":{"result":"Sunny, 21°C"}}}`}},
	}

	for _, c := range cases {
		request, err := c.protocol.request(context.Background(), endpoint{baseURL: "http://example.test", model: "m"}, prompt, true, false)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(request.Body)
		for _, fragment := range c.want {
			if !strings.Contains(string(body), fragment) {
				t.Errorf("%s: request lacks %s\n%s", c.name, fragment, body)
			}
		}
	}
}

func TestAModelThatRefusesToolsIsAskedAgainWithoutThem(t *testing.T) {
	var withTools, without int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"tools"`) {
			withTools++
			http.Error(w, `{"error":{"message":"this model does not support tools"}}`, http.StatusBadRequest)
			return
		}
		without++
		sse(`{"choices":[{"delta":{"content":"Sunny"}}]}`, `[DONE]`)(w, r)
	}))
	defer server.Close()
	p, _ := FromSettings("local", config.ProviderSettings{BaseURL: server.URL + "/v1", Model: "no-tools"}, "", true)

	for range 2 {
		reply, err := p.(ToolUser).Turn(context.Background(), Prompt{Text: "Weather?", Tools: []Tool{weatherTool}}, func(string) {})
		if err != nil || reply.Text != "Sunny" {
			t.Fatalf("got %q, %v", reply.Text, err)
		}
	}
	if withTools != 1 || without != 2 {
		t.Fatalf("sent tools %d times and none %d times, want the refusal remembered after once", withTools, without)
	}
}

func TestTheLastRoundForbidsToolCallsInEachProtocol(t *testing.T) {
	prompt := Prompt{Text: "q", Tools: []Tool{weatherTool}, NoMoreCalls: true}
	cases := []struct {
		name     string
		protocol protocol
		want     string
	}{
		{"openai", chatCompletions{}, `"tool_choice":"none"`},
		{"anthropic", messages{}, `"tool_choice":{"type":"none"}`},
		{"gemini", generateContent{}, `"toolConfig":{"functionCallingConfig":{"mode":"NONE"}}`},
	}
	for _, c := range cases {
		request, err := c.protocol.request(context.Background(), endpoint{baseURL: "http://example.test", model: "m"}, prompt, true, false)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), c.want) {
			t.Errorf("%s: request lacks %s\n%s", c.name, c.want, body)
		}
	}
}

func TestARefusalAfterAToolRanIsAnErrorNotADroppedLookup(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, `{"error":{"message":"context too long"}}`, http.StatusBadRequest)
	}))
	defer server.Close()
	p, _ := FromSettings("local", config.ProviderSettings{BaseURL: server.URL + "/v1", Model: "later-refusal"}, "", true)

	prompt := Prompt{Text: "q", Tools: []Tool{weatherTool}, Steps: []Step{{Calls: []ToolCall{{ID: "1", Name: "get_weather"}}, Results: []string{"Sunny"}}}}
	if _, err := p.(ToolUser).Turn(context.Background(), prompt, func(string) {}); err == nil {
		t.Fatal("a refused second round was answered without its lookups")
	}
	if requests != 1 {
		t.Fatalf("sent %d requests, want the one, without retrying it stripped", requests)
	}
	if _, refused := refusedTools.Load(server.URL + "/v1::later-refusal"); refused {
		t.Fatal("a model that took tools was marked as refusing them")
	}
}

func TestCallsWithoutAnIndexStaySeparate(t *testing.T) {
	server := httptest.NewServer(sse(
		`{"choices":[{"delta":{"tool_calls":[{"id":"a","type":"function","function":{"name":"web_search","arguments":"{\"query\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"\"go\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"id":"b","type":"function","function":{"name":"get_weather","arguments":"{\"place\":\"Paris\"}"}}]}}]}`,
		`[DONE]`,
	))
	defer server.Close()
	p, _ := FromSettings("local", config.ProviderSettings{BaseURL: server.URL + "/v1"}, "", true)

	reply, err := p.(ToolUser).Turn(context.Background(), Prompt{Text: "q", Tools: []Tool{weatherTool}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Calls) != 2 || string(reply.Calls[0].Arguments) != `{"query":"go"}` || string(reply.Calls[1].Arguments) != `{"place":"Paris"}` {
		t.Fatalf("read the calls as %+v", reply.Calls)
	}
}

func TestTheContextGoesWithTheQuestion(t *testing.T) {
	messages := Prompt{System: "s", Text: "What time is it?", Context: "Local time: 14:05"}.conversation("assistant")
	if last := messages[len(messages)-1].Content; last != "What time is it?\n\nLocal time: 14:05" {
		t.Fatalf("the question reads %q", last)
	}
}
