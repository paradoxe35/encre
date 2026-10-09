package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// chatCompletions is OpenAI's Chat Completions API, which OpenRouter and the local servers speak too.
type chatCompletions struct{}

type chatRequest struct {
	Model           string               `json:"model"`
	Messages        []chatMessage        `json:"messages"`
	Tools           []chatTool           `json:"tools,omitempty"`
	ToolChoice      string               `json:"tool_choice,omitempty"`
	Temperature     float64              `json:"temperature"`
	Stream          bool                 `json:"stream,omitempty"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
	Reasoning       *openRouterReasoning `json:"reasoning,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Arguments   string         `json:"arguments,omitempty"`
}

// chatToolCall is a call in a reply, streamed in pieces under its index, or sent back in a request.
type chatToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function chatFunction `json:"function"`
}

// OpenRouter normalises reasoning across every model it serves, and refuses the OpenAI field beside it.
type openRouterReasoning struct {
	Effort  string `json:"effort"`
	Exclude bool   `json:"exclude"`
}

type chatReply struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		Delta        chatMessage `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Error *replyError `json:"error"`
}

func (chatCompletions) request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error) {
	body := chatRequest{
		Model:       target.model,
		Messages:    append([]chatMessage{{Role: "system", Content: prompt.System}}, prompt.conversation("assistant")...),
		Temperature: target.temperature,
		Stream:      stream,
	}
	for _, tool := range prompt.Tools {
		body.Tools = append(body.Tools, chatTool{Type: "function", Function: chatFunction{
			Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters,
		}})
	}
	if prompt.NoMoreCalls && len(body.Tools) > 0 {
		body.ToolChoice = "none"
	}
	for _, step := range prompt.Steps {
		calls := make([]chatToolCall, len(step.Calls))
		for i, call := range step.Calls {
			calls[i] = chatToolCall{ID: call.ID, Type: "function", Function: chatFunction{
				Name: call.Name, Arguments: string(call.arguments()),
			}}
		}
		body.Messages = append(body.Messages, chatMessage{Role: "assistant", ToolCalls: calls})
		for i, result := range step.Results {
			body.Messages = append(body.Messages, chatMessage{Role: "tool", ToolCallID: step.Calls[i].ID, Content: result})
		}
	}
	// "low" rather than "none": the widest range of models accept it, and some cannot stop reasoning.
	if lowReasoning {
		if isOpenRouter(target.baseURL) {
			body.Reasoning = &openRouterReasoning{Effort: "low", Exclude: true}
		} else {
			body.ReasoningEffort = "low"
		}
	}

	headers := map[string]string{}
	// A local server takes no key, and some reject an empty bearer.
	if target.apiKey != "" {
		headers["Authorization"] = "Bearer " + target.apiKey
	}
	return newJSONRequest(ctx, target.baseURL+"/chat/completions", body, headers)
}

func (chatCompletions) decode(body []byte) (string, error) {
	var reply chatReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", err
	}
	if err := reply.Error.err(); err != nil {
		return "", err
	}
	if len(reply.Choices) == 0 {
		return "", errNoReply
	}
	if reply.Choices[0].FinishReason == "length" {
		return "", errLengthLimit
	}
	return reply.Choices[0].Message.Content, nil
}

func (chatCompletions) event(data []byte, reply *turn) (bool, error) {
	if string(data) == "[DONE]" {
		return true, nil
	}
	var chunk chatReply
	if err := json.Unmarshal(data, &chunk); err != nil {
		return false, err
	}
	if err := chunk.Error.err(); err != nil {
		return false, err
	}
	if len(chunk.Choices) == 0 {
		return false, nil
	}
	choice := chunk.Choices[0]
	reply.write(choice.Delta.Content)
	for _, piece := range choice.Delta.ToolCalls {
		call := reply.call(chatCallIndex(piece, reply))
		if piece.ID != "" {
			call.ID = piece.ID
		}
		if piece.Function.Name != "" {
			call.Name = piece.Function.Name
		}
		call.Arguments = append(call.Arguments, piece.Function.Arguments...)
	}
	if choice.FinishReason == "length" {
		return false, errLengthLimit
	}
	return false, nil
}

// chatCallIndex is where a streamed piece belongs. Some servers leave the index out: then a new ID
// starts a new call, and a piece without one continues the last.
func chatCallIndex(piece chatToolCall, reply *turn) int {
	if piece.Index != nil {
		return *piece.Index
	}
	last := len(reply.calls) - 1
	if last < 0 || (piece.ID != "" && piece.ID != reply.calls[last].ID) {
		return len(reply.calls)
	}
	return last
}

func isOpenRouter(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "openrouter.ai")
}
