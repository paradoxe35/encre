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
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  geminiConfig    `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text    string `json:"text"`
	Thought bool   `json:"thought,omitempty"`
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
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *replyError `json:"error"`
}

func (generateContent) request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error) {
	body := geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: prompt.System}}},
		Contents:          []geminiContent{{Role: "user", Parts: []geminiPart{{Text: prompt.Text}}}},
		GenerationConfig:  geminiConfig{Temperature: target.temperature},
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
	if err == nil && text == "" {
		return "", errNoReply
	}
	return text, err
}

func (generateContent) event(data []byte) (string, bool, error) {
	text, err := geminiText(data)
	return text, false, err
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

	var text strings.Builder
	for _, part := range reply.Candidates[0].Content.Parts {
		if !part.Thought {
			text.WriteString(part.Text)
		}
	}
	return text.String(), nil
}
