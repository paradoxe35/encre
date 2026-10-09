package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// generateContent is the Gemini API.
type generateContent struct{}

type geminiRequest struct {
	SystemInstruction *geminiContent    `json:"systemInstruction,omitempty"`
	Contents          []geminiContent   `json:"contents"`
	Tools             []geminiTools     `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig `json:"toolConfig,omitempty"`
	GenerationConfig  geminiConfig      `json:"generationConfig"`
}

type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode string `json:"mode"`
	} `json:"functionCallingConfig"`
}

type geminiTools struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations"`
}

type geminiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiResult struct {
	ID       string            `json:"id,omitempty"`
	Name     string            `json:"name"`
	Response map[string]string `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string        `json:"text,omitempty"`
	Thought          bool          `json:"thought,omitempty"`
	FunctionCall     *geminiCall   `json:"functionCall,omitempty"`
	FunctionResponse *geminiResult `json:"functionResponse,omitempty"`
	ThoughtSignature string        `json:"thoughtSignature,omitempty"`
}

type geminiConfig struct {
	Temperature    float64         `json:"temperature"`
	ThinkingConfig *geminiThinking `json:"thinkingConfig,omitempty"`
}

type geminiThinking struct {
	ThinkingBudget *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel  string `json:"thinkingLevel,omitempty"`
}

type geminiReply struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	Error *replyError `json:"error"`
}

func (generateContent) request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error) {
	body := geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: prompt.System}}},
		Contents:          geminiContents(prompt.conversation("model")),
		GenerationConfig:  geminiConfig{Temperature: target.temperature},
	}
	if len(prompt.Tools) > 0 {
		declarations := make([]geminiFunction, len(prompt.Tools))
		for i, tool := range prompt.Tools {
			declarations[i] = geminiFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}
		}
		body.Tools = []geminiTools{{FunctionDeclarations: declarations}}
		if prompt.NoMoreCalls {
			body.ToolConfig = &geminiToolConfig{}
			body.ToolConfig.FunctionCallingConfig.Mode = "NONE"
		}
	}
	for _, step := range prompt.Steps {
		calls := make([]geminiPart, len(step.Calls))
		results := make([]geminiPart, len(step.Results))
		for i, call := range step.Calls {
			calls[i] = geminiPart{
				FunctionCall:     &geminiCall{ID: call.ID, Name: call.Name, Args: call.arguments()},
				ThoughtSignature: call.signature,
			}
			results[i] = geminiPart{FunctionResponse: &geminiResult{
				ID: call.ID, Name: call.Name, Response: map[string]string{"result": step.Results[i]},
			}}
		}
		body.Contents = append(body.Contents,
			geminiContent{Role: "model", Parts: calls},
			geminiContent{Role: "user", Parts: results},
		)
	}
	if lowReasoning {
		body.GenerationConfig.ThinkingConfig = lowThinking(target.model)
	}

	method := ":generateContent"
	if stream {
		method = ":streamGenerateContent?alt=sse"
	}
	url := target.baseURL + "/v1beta/models/" + target.model + method
	return newJSONRequest(ctx, url, body, map[string]string{"x-goog-api-key": target.apiKey})
}

func geminiContents(messages []chatMessage) []geminiContent {
	contents := make([]geminiContent, len(messages))
	for i, message := range messages {
		contents[i] = geminiContent{Role: message.Role, Parts: []geminiPart{{Text: message.Content}}}
	}
	return contents
}

// Gemini 2 takes a thinking budget and later models a level; each refuses the other's field.
func lowThinking(model string) *geminiThinking {
	if strings.HasPrefix(model, "gemini-2") {
		none := 0
		return &geminiThinking{ThinkingBudget: &none}
	}
	return &geminiThinking{ThinkingLevel: "low"}
}

func (generateContent) decode(body []byte) (string, error) {
	text, err := geminiText(body)
	if errors.Is(err, errLengthLimit) {
		return "", err
	}
	if err == nil && text == "" {
		return "", errNoReply
	}
	return text, err
}

func (generateContent) event(data []byte, reply *turn) (bool, error) {
	var chunk geminiReply
	if err := json.Unmarshal(data, &chunk); err != nil {
		return false, err
	}
	if err := chunk.Error.err(); err != nil {
		return false, err
	}
	if len(chunk.Candidates) == 0 {
		return false, nil
	}
	candidate := chunk.Candidates[0]
	for _, part := range candidate.Content.Parts {
		switch {
		case part.FunctionCall != nil:
			reply.add(ToolCall{
				ID: part.FunctionCall.ID, Name: part.FunctionCall.Name,
				Arguments: part.FunctionCall.Args, signature: part.ThoughtSignature,
			})
		case !part.Thought:
			reply.write(part.Text)
		}
	}
	if candidate.FinishReason == "MAX_TOKENS" {
		return false, errLengthLimit
	}
	return false, nil
}

func geminiText(data []byte) (string, error) {
	var reply geminiReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", err
	}
	if err := reply.Error.err(); err != nil {
		return "", err
	}
	if len(reply.Candidates) == 0 {
		return "", nil
	}

	candidate := reply.Candidates[0]
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		if !part.Thought {
			text.WriteString(part.Text)
		}
	}
	if candidate.FinishReason == "MAX_TOKENS" {
		return text.String(), errLengthLimit
	}
	return text.String(), nil
}
