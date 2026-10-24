package ai

import (
	"context"
	"encoding/json"
	"net/http"
)

// messages is Anthropic's Messages API.
type messages struct{}

// The allowance covers thinking too, which current models do by default.
const (
	anthropicVersion = "2023-06-01"
	streamMaxTokens  = 64000
	// A reply sent whole arrives only once written, so it is kept shorter.
	wholeMaxTokens = 16000
)

type messagesRequest struct {
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	System       string             `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	Tools        []anthropicTool    `json:"tools,omitempty"`
	ToolChoice   *anthropicChoice   `json:"tool_choice,omitempty"`
	Temperature  *float64           `json:"temperature,omitempty"`
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
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
}

// anthropicThinking is a thinking or redacted_thinking block, sent back exactly as it came.
type anthropicThinking struct {
	Type      string
	Thinking  string
	Signature string
	Data      string
}

func (b anthropicThinking) MarshalJSON() ([]byte, error) {
	if b.Type == "redacted_thinking" {
		return json.Marshal(struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}{b.Type, b.Data})
	}
	return json.Marshal(struct {
		Type      string `json:"type"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
	}{b.Type, b.Thinking, b.Signature})
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
	Thinking    string `json:"thinking"`
	Signature   string `json:"signature"`
	StopReason  string `json:"stop_reason"`
}

type messagesReply struct {
	Type         string           `json:"type"`
	Index        int              `json:"index"`
	ContentBlock anthropicBlock   `json:"content_block"`
	Content      []anthropicBlock `json:"content"`
	Delta        messagesDelta    `json:"delta"`
	StopReason   string           `json:"stop_reason"`
	Error        *replyError      `json:"error"`
}

func anthropicStop(reason string) error {
	switch reason {
	case "max_tokens", "model_context_window_exceeded":
		return errLengthLimit
	case "refusal":
		return errRefused
	}
	return nil
}

func (messages) request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error) {
	body := messagesRequest{
		Model:     target.model,
		MaxTokens: wholeMaxTokens,
		System:    prompt.system(),
		Stream:    stream,
	}
	if stream {
		body.MaxTokens = streamMaxTokens
	}
	if target.maxTokens > 0 {
		body.MaxTokens = min(body.MaxTokens, target.maxTokens)
	}
	if target.temperature != defaultTemperature {
		body.Temperature = &target.temperature
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
		var calls []any
		results := make([]anthropicBlock, len(step.Results))
		for i, call := range step.Calls {
			for _, thought := range call.thinking {
				calls = append(calls, thought)
			}
			calls = append(calls, anthropicBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: call.arguments()})
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

func (messages) decode(body []byte, reply *turn) error {
	var whole messagesReply
	if err := json.Unmarshal(body, &whole); err != nil {
		return err
	}
	if err := whole.Error.err(); err != nil {
		return err
	}
	if err := anthropicStop(whole.StopReason); err != nil {
		return err
	}
	for i, block := range whole.Content {
		if call := anthropicStart(i, block, reply); call != nil {
			call.Arguments = block.Input
		}
	}
	if reply.empty() {
		return errNoReply
	}
	return nil
}

func (messages) event(data []byte, reply *turn) (bool, error) {
	var event messagesReply
	if err := json.Unmarshal(data, &event); err != nil {
		return false, err
	}

	switch event.Type {
	case "content_block_start":
		anthropicStart(event.Index, event.ContentBlock, reply)
	case "content_block_delta":
		switch event.Delta.Type {
		case "text_delta":
			reply.write(event.Delta.Text)
		case "input_json_delta":
			call := reply.call(event.Index)
			call.Arguments = append(call.Arguments, event.Delta.PartialJSON...)
		case "thinking_delta":
			if thought := reply.thinking[event.Index]; thought != nil {
				thought.Thinking += event.Delta.Thinking
			}
		case "signature_delta":
			if thought := reply.thinking[event.Index]; thought != nil {
				thought.Signature += event.Delta.Signature
			}
		}
	case "message_delta":
		if err := anthropicStop(event.Delta.StopReason); err != nil {
			return false, err
		}
	case "message_stop":
		return true, nil
	case "error":
		return false, event.Error.err()
	}
	return false, nil
}

// anthropicStart reads a content block as it opens, and returns the call it starts, if any.
func anthropicStart(index int, block anthropicBlock, reply *turn) *ToolCall {
	switch block.Type {
	case "text":
		reply.write(block.Text)
	case "tool_use":
		call := reply.call(index)
		call.ID, call.Name = block.ID, block.Name
		call.thinking = reply.claimThinking()
		return call
	case "thinking", "redacted_thinking":
		reply.think(index, anthropicThinking{Type: block.Type, Thinking: block.Thinking, Signature: block.Signature, Data: block.Data})
	}
	return nil
}

func (t *turn) think(index int, thought anthropicThinking) {
	if t.thinking == nil {
		t.thinking = make(map[int]*anthropicThinking)
	}
	t.thinking[index] = &thought
	t.order = append(t.order, index)
}

// claimThinking hands over, in order, the thinking streamed since the last call.
func (t *turn) claimThinking() []anthropicThinking {
	var thoughts []anthropicThinking
	for _, index := range t.order {
		thoughts = append(thoughts, *t.thinking[index])
		delete(t.thinking, index)
	}
	t.order = nil
	return thoughts
}
