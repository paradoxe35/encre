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
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	System       string             `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	Tools        []anthropicTool    `json:"tools,omitempty"`
	ToolChoice   *anthropicChoice   `json:"tool_choice,omitempty"`
	Temperature  float64            `json:"temperature"`
	Stream       bool               `json:"stream,omitempty"`
	OutputConfig *anthropicOutput   `json:"output_config,omitempty"`
}

// Effort covers thinking and text alike; models before Opus 4.5 and Sonnet 4.6 refuse it.
type anthropicOutput struct {
	Effort string `json:"effort"`
}

// anthropicMessage holds text, or the content blocks of tool calls and their results.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicChoice struct {
	Type string `json:"type"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type messagesDelta struct {
	textBlock
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

type messagesReply struct {
	Type         string         `json:"type"`
	Index        int            `json:"index"`
	ContentBlock anthropicBlock `json:"content_block"`
	Content      []textBlock    `json:"content"`
	Delta        messagesDelta  `json:"delta"`
	StopReason   string         `json:"stop_reason"`
	Error        *replyError    `json:"error"`
}

const anthropicLengthLimit = "max_tokens"

func (messages) request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error) {
	body := messagesRequest{
		Model:       target.model,
		MaxTokens:   anthropicMaxTokens,
		System:      prompt.system(),
		Temperature: target.temperature,
		Stream:      stream,
	}
	for _, message := range prompt.conversation("assistant") {
		body.Messages = append(body.Messages, anthropicMessage{Role: message.Role, Content: message.Content})
	}
	for _, tool := range prompt.Tools {
		body.Tools = append(body.Tools, anthropicTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters})
	}
	if prompt.NoMoreCalls && len(body.Tools) > 0 {
		body.ToolChoice = &anthropicChoice{Type: "none"}
	}
	for _, step := range prompt.Steps {
		calls := make([]anthropicBlock, len(step.Calls))
		results := make([]anthropicBlock, len(step.Results))
		for i, call := range step.Calls {
			calls[i] = anthropicBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: call.arguments()}
			results[i] = anthropicBlock{Type: "tool_result", ToolUseID: call.ID, Content: step.Results[i]}
		}
		body.Messages = append(body.Messages,
			anthropicMessage{Role: "assistant", Content: calls},
			anthropicMessage{Role: "user", Content: results},
		)
	}
	if lowReasoning {
		body.OutputConfig = &anthropicOutput{Effort: "low"}
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
	if reply.StopReason == anthropicLengthLimit {
		return "", errLengthLimit
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

func (messages) event(data []byte, reply *turn) (bool, error) {
	var event messagesReply
	if err := json.Unmarshal(data, &event); err != nil {
		return false, err
	}

	switch event.Type {
	case "content_block_start":
		if event.ContentBlock.Type == "tool_use" {
			call := reply.call(event.Index)
			call.ID, call.Name = event.ContentBlock.ID, event.ContentBlock.Name
		}
	case "content_block_delta":
		switch event.Delta.Type {
		case "text_delta":
			reply.write(event.Delta.Text)
		case "input_json_delta":
			call := reply.call(event.Index)
			call.Arguments = append(call.Arguments, event.Delta.PartialJSON...)
		}
	case "message_delta":
		if event.Delta.StopReason == anthropicLengthLimit {
			return false, errLengthLimit
		}
	case "message_stop":
		return true, nil
	case "error":
		return false, event.Error.err()
	}
	return false, nil
}
