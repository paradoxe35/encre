package ai

import (
	"context"
	"encoding/json"
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

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiReply struct {
	Candidates     []geminiCandidate `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *replyError `json:"error"`
}

func (generateContent) request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error) {
	body := geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: prompt.system()}}},
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

func (generateContent) decode(body []byte, reply *turn) error {
	candidate, err := geminiFirst(body)
	if err != nil {
		return err
	}
	if candidate != nil {
		if err := geminiStop(candidate.FinishReason); err != nil {
			return err
		}
		geminiParts(candidate.Content.Parts, reply)
	}
	if reply.empty() {
		return errNoReply
	}
	return nil
}

func (generateContent) event(data []byte, reply *turn) (bool, error) {
	candidate, err := geminiFirst(data)
	if err != nil || candidate == nil {
		return false, err
	}
	geminiParts(candidate.Content.Parts, reply)
	reply.ended = reply.ended || candidate.FinishReason != ""
	return false, geminiStop(candidate.FinishReason)
}

func geminiFirst(data []byte) (*geminiCandidate, error) {
	var reply geminiReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, err
	}
	if err := reply.Error.err(); err != nil {
		return nil, err
	}
	if reply.PromptFeedback.BlockReason != "" {
		return nil, errFiltered
	}
	if len(reply.Candidates) == 0 {
		return nil, nil
	}
	return &reply.Candidates[0], nil
}

func geminiParts(parts []geminiPart, reply *turn) {
	for _, part := range parts {
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
}

func geminiStop(reason string) error {
	switch reason {
	case "MAX_TOKENS":
		return errLengthLimit
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		return errFiltered
	}
	return nil
}
