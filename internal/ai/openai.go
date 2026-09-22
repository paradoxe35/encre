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

type OpenAIProvider struct {
	// Name is what errors call this endpoint: "openai" unless a custom provider lends its own.
	Name        string
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float64
	// LowReasoning asks a reasoning model to think less. A correction is not a puzzle, and the
	// tokens it spends thinking are billed and thrown away.
	LowReasoning bool
	client       *http.Client
}

func (p *OpenAIProvider) SetLowReasoning(low bool) { p.LowReasoning = low }

const openAIBaseURL = "https://api.openai.com/v1"

func NewOpenAIProvider(apiKey, baseURL, model string, temperature float64) *OpenAIProvider {
	if baseURL == "" {
		baseURL = openAIBaseURL
	}
	if model == "" {
		model = "gpt-6-luna"
	}
	if temperature == 0 {
		temperature = 1.0
	}
	return &OpenAIProvider{
		Name:        "openai",
		APIKey:      apiKey,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Model:       model,
		Temperature: temperature,
		client:      &http.Client{},
	}
}

type OpenAIRequest struct {
	Model           string               `json:"model"`
	Messages        []OpenAIMessage      `json:"messages"`
	Temperature     float64              `json:"temperature"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
	Reasoning       *OpenRouterReasoning `json:"reasoning,omitempty"`
}

// OpenRouterReasoning is OpenRouter's own shape, which it normalises across every model it serves.
type OpenRouterReasoning struct {
	Effort string `json:"effort"`
	// Reasoning tokens are billed and useless here: only the rewritten text is wanted.
	Exclude bool `json:"exclude"`
}

type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message OpenAIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string      `json:"message"`
		Type    string      `json:"type"`
		Code    interface{} `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

func (p *OpenAIProvider) ReviseText(ctx context.Context, text, systemPrompt string) (string, error) {
	if err := p.ValidateConfig(); err != nil {
		return "", err
	}

	messages := []OpenAIMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: text},
	}

	return withReasoningFallback(p.BaseURL, p.Model, p.LowReasoning, func(includeReasoning bool) (string, error) {
		body := OpenAIRequest{Model: p.Model, Messages: messages, Temperature: p.Temperature}
		if includeReasoning {
			// "low" rather than "none": it is the value the widest range of models accept, and
			// some refuse to have reasoning switched off entirely.
			switch DetectReasoningStyle(p.BaseURL) {
			case ReasoningOpenRouter:
				body.Reasoning = &OpenRouterReasoning{Effort: "low", Exclude: true}
			default:
				body.ReasoningEffort = "low"
			}
		}
		return p.send(ctx, body)
	})
}

func (p *OpenAIProvider) send(ctx context.Context, requestBody OpenAIRequest) (string, error) {
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.BaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// A local runtime takes no credentials, and an empty bearer is worse than none: some reject it.
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

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
		return "", ParseAPIError(resp.StatusCode, body, p.Name)
	}

	var response OpenAIResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", ParseUnmarshalError(err, body, resp.StatusCode, p.Name)
	}

	if response.Error != nil {
		return "", fmt.Errorf("API error: %s", response.Error.Message)
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no response from API")
	}

	return response.Choices[0].Message.Content, nil
}

func (p *OpenAIProvider) ValidateConfig() error {
	if p.BaseURL == "" {
		return fmt.Errorf("OpenAI base URL is required")
	}
	return nil
}

func (p *OpenAIProvider) GetName() string {
	return p.Name
}

func (p *OpenAIProvider) GetModel() string {
	return p.Model
}
