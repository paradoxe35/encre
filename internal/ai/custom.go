package ai

import (
	"fmt"

	"github.com/paradoxe35/encre/internal/config"
)

// A custom provider is an OpenAI-compatible endpoint that answers to its own name in errors.
// Rejects a providerType it cannot serve rather than coercing it. Empty is accepted so a
// config without the field still loads.
func NewCustomProvider(name, providerType, apiKey, baseURL, model string, temperature float64) (*OpenAIProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required for custom providers")
	}
	if providerType != "" && providerType != config.ProviderTypeOpenAICompatible {
		return nil, fmt.Errorf("unsupported custom provider type: %s", providerType)
	}

	provider := NewOpenAIProvider(apiKey, baseURL, model, temperature)
	provider.Name = name
	return provider, nil
}
