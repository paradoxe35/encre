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

type AnthropicProvider struct {
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float64
	client      *http.Client
}

const (
	anthropicBaseURL = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
)

func NewAnthropicProvider(apiKey, baseURL, model string, temperature float64) *AnthropicProvider {
	if baseURL == "" {
		baseURL = anthropicBaseURL
	}
	if model == "" {
		model = "claude-haiku-4-5"
	}
	if temperature == 0 {
		temperature = 1.0
	}
	return &AnthropicProvider{
		APIKey:      apiKey,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Model:       model,
		Temperature: temperature,
		client:      &http.Client{},
	}
}

type AnthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []AnthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Temperature float64            `json:"temperature"`
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AnthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *AnthropicProvider) ReviseText(ctx context.Context, text, systemPrompt string) (string, error) {
	if err := p.ValidateConfig(); err != nil {
		return "", err
	}

	messages := []AnthropicMessage{
		{Role: "user", Content: text},
	}

	requestBody := AnthropicRequest{
		Model:       p.Model,
		Messages:    messages,
		MaxTokens:   4096,
		System:      systemPrompt,
		Temperature: p.Temperature,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.BaseURL+"/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

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
		return "", ParseAPIError(resp.StatusCode, body, "claude")
	}

	var response AnthropicResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", ParseUnmarshalError(err, body, resp.StatusCode, "claude")
	}

	if response.Error != nil {
		return "", fmt.Errorf("API error: %s", response.Error.Message)
	}

	if len(response.Content) == 0 {
		return "", fmt.Errorf("no response from API")
	}

	var result strings.Builder
	for _, content := range response.Content {
		if content.Type == "text" {
			result.WriteString(content.Text)
		}
	}

	return result.String(), nil
}

func (p *AnthropicProvider) ValidateConfig() error {
	if p.APIKey == "" {
		return fmt.Errorf("anthropic API key is required")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("anthropic base URL is required")
	}
	return nil
}

func (p *AnthropicProvider) GetName() string {
	return "claude"
}

func (p *AnthropicProvider) GetModel() string {
	return p.Model
}
