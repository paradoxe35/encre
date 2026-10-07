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
	Temperature     float64              `json:"temperature"`
	Stream          bool                 `json:"stream,omitempty"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
	Reasoning       *openRouterReasoning `json:"reasoning,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
		Model: target.model,
		Messages: []chatMessage{
			{Role: "system", Content: prompt.System},
			{Role: "user", Content: prompt.Text},
		},
		Temperature: target.temperature,
		Stream:      stream,
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

func (chatCompletions) event(data []byte) (string, bool, error) {
	if string(data) == "[DONE]" {
		return "", true, nil
	}
	var reply chatReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", false, err
	}
	if err := reply.Error.err(); err != nil {
		return "", false, err
	}
	if len(reply.Choices) == 0 {
		return "", false, nil
	}
	choice := reply.Choices[0]
	if choice.FinishReason == "length" {
		return choice.Delta.Content, false, errLengthLimit
	}
	return choice.Delta.Content, false, nil
}

func isOpenRouter(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "openrouter.ai")
}
