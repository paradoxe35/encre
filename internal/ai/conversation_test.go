package ai

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
)

func TestEachProtocolSendsTheEarlierTurnsBeforeTheQuestion(t *testing.T) {
	prompt := Prompt{
		System:  "Be brief.",
		History: []Turn{{Question: "Capital of France?", Answer: "Paris."}},
		Text:    "And its population?",
	}
	cases := []struct {
		name     string
		protocol protocol
		read     func(body []byte) []string
	}{
		{"openai", chatCompletions{}, func(body []byte) []string {
			var request chatRequest
			json.Unmarshal(body, &request)
			return roles(request.Messages)
		}},
		{"anthropic", messages{}, func(body []byte) []string {
			var request messagesRequest
			json.Unmarshal(body, &request)
			return roles(request.Messages)
		}},
		{"gemini", generateContent{}, func(body []byte) []string {
			var request geminiRequest
			json.Unmarshal(body, &request)
			var got []string
			for _, content := range request.Contents {
				got = append(got, content.Role+": "+content.Parts[0].Text)
			}
			return got
		}},
	}
	want := map[string][]string{
		"openai":    {"system: Be brief.", "user: Capital of France?", "assistant: Paris.", "user: And its population?"},
		"anthropic": {"user: Capital of France?", "assistant: Paris.", "user: And its population?"},
		"gemini":    {"user: Capital of France?", "model: Paris.", "user: And its population?"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			request, err := c.protocol.request(context.Background(), endpoint{baseURL: "http://example.test", model: "m"}, prompt, false, false)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(request.Body)
			if got := c.read(body); !reflect.DeepEqual(got, want[c.name]) {
				t.Fatalf("sent %q, want %q", got, want[c.name])
			}
		})
	}
}

func roles(messages []chatMessage) []string {
	got := make([]string, len(messages))
	for i, message := range messages {
		got[i] = message.Role + ": " + message.Content
	}
	return got
}
