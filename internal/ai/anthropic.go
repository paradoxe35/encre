package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// messages is Anthropic's Messages API.
type messages struct{}

const (
	anthropicVersion   = "2023-06-01"
	anthropicMaxTokens = 4096
)

type messagesRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	System      string        `json:"system,omitempty"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream,omitempty"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type messagesReply struct {
	Type    string      `json:"type"`
	Content []textBlock `json:"content"`
	Delta   textBlock   `json:"delta"`
	Error   *replyError `json:"error"`
}

func (messages) request(ctx context.Context, target endpoint, prompt Prompt, stream, _ bool) (*http.Request, error) {
	body := messagesRequest{
		Model:       target.model,
		MaxTokens:   anthropicMaxTokens,
		System:      prompt.System,
		Messages:    []chatMessage{{Role: "user", Content: prompt.Text}},
		Temperature: target.temperature,
		Stream:      stream,
	}
	return newJSONRequest(ctx, target.baseURL+"/v1/messages", body, anthropicHeaders(target.apiKey))
}

func anthropicHeaders(apiKey string) map[string]string {
	return map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": anthropicVersion,
	}
}

func (messages) decode(body []byte) (string, error) {
	var reply messagesReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", err
	}
	if err := reply.Error.err(); err != nil {
		return "", err
	}

	var text strings.Builder
	for _, block := range reply.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if text.Len() == 0 {
		return "", errNoReply
	}
	return text.String(), nil
}

func (messages) event(data []byte) (string, bool, error) {
	var reply messagesReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", false, err
	}

	switch reply.Type {
	case "content_block_delta":
		if reply.Delta.Type == "text_delta" {
			return reply.Delta.Text, false, nil
		}
	case "message_stop":
		return "", true, nil
	case "error":
		return "", false, reply.Error.err()
	}
	return "", false, nil
}
