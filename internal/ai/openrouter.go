package ai

import "github.com/paradoxe35/encre/internal/config"

const openRouterBaseURL = "https://openrouter.ai/api/v1"

// NewOpenRouterProvider is the OpenAI client pointed at OpenRouter, which serves every
// major model behind one key and one protocol.
func NewOpenRouterProvider(apiKey, baseURL, model string, temperature float64) *OpenAIProvider {
	if baseURL == "" {
		baseURL = openRouterBaseURL
	}
	provider := NewOpenAIProvider(apiKey, baseURL, model, temperature)
	provider.Name = config.BuiltInOpenRouter
	return provider
}
