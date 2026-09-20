package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type GeminiProvider struct {
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float64
	// LowReasoning asks the model to think less. A correction is not a puzzle, and the tokens it
	// spends thinking are billed and thrown away.
	LowReasoning bool
	client       *http.Client
}

func (p *GeminiProvider) SetLowReasoning(low bool) { p.LowReasoning = low }

const geminiBaseURL = "https://generativelanguage.googleapis.com"

func NewGeminiProvider(apiKey, baseURL, model string, temperature float64) *GeminiProvider {
	if baseURL == "" {
		baseURL = geminiBaseURL
	}
	if model == "" {
		model = "gemini-2.5-flash-lite"
	}
	if temperature == 0 {
		temperature = 1.0
	}
	return &GeminiProvider{
		APIKey:      apiKey,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Model:       model,
		Temperature: temperature,
		client:      &http.Client{},
	}
}

type ThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type GenerationConfig struct {
	ThinkingConfig *ThinkingConfig `json:"thinkingConfig,omitempty"`
	Temperature    float64         `json:"temperature"`
}

type GeminiRequest struct {
	Contents         []GeminiContent  `json:"contents"`
	GenerationConfig GenerationConfig `json:"generationConfig"`
}

type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content GeminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

func (p *GeminiProvider) ReviseText(ctx context.Context, text, systemPrompt string) (string, error) {
	if err := p.ValidateConfig(); err != nil {
		return "", err
	}

	fullText := fmt.Sprintf("%s\n\n%s", systemPrompt, text)

	contents := []GeminiContent{
		{
			Parts: []GeminiPart{
				{Text: fullText},
			},
		},
	}

	// A zero budget is refused outright by the models that cannot switch thinking off, so the
	// request is retried without it rather than failing the user's correction.
	return withReasoningFallback(p.BaseURL, p.Model, p.LowReasoning, func(includeReasoning bool) (string, error) {
		config := GenerationConfig{Temperature: p.Temperature}
		if includeReasoning {
			config.ThinkingConfig = &ThinkingConfig{ThinkingBudget: 0}
		}
		return p.send(ctx, GeminiRequest{Contents: contents, GenerationConfig: config})
	})
}

func (p *GeminiProvider) send(ctx context.Context, requestBody GeminiRequest) (string, error) {
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", p.BaseURL, p.Model)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.APIKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ParseAPIError(resp.StatusCode, body, "gemini")
	}

	var response GeminiResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", ParseUnmarshalError(err, body, resp.StatusCode, "gemini")
	}

	if response.Error != nil {
		return "", fmt.Errorf("API error: %s", response.Error.Message)
	}

	if len(response.Candidates) == 0 {
		return "", fmt.Errorf("no response from API")
	}

	var result strings.Builder
	for _, part := range response.Candidates[0].Content.Parts {
		result.WriteString(part.Text)
	}

	return result.String(), nil
}

func (p *GeminiProvider) ValidateConfig() error {
	if p.APIKey == "" {
		return fmt.Errorf("gemini API key is required")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("gemini base URL is required")
	}
	return nil
}

func (p *GeminiProvider) GetName() string {
	return "gemini"
}

func (p *GeminiProvider) GetModel() string {
	return p.Model
}
