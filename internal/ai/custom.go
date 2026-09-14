package ai

import (
	"context"
	"fmt"

	"github.com/paradoxe35/encre/internal/config"
)

type CustomProvider struct {
	name  string
	inner Provider
}

// NewCustomProvider rejects a providerType it cannot serve rather than silently coercing it.
// Empty is accepted: configs written before this field existed have nothing to check.
func NewCustomProvider(name, providerType, apiKey, baseURL, model string, temperature float64) (*CustomProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required for custom providers")
	}
	if providerType != "" && providerType != config.ProviderTypeOpenAICompatible {
		return nil, fmt.Errorf("unsupported custom provider type: %s", providerType)
	}

	return &CustomProvider{
		name:  name,
		inner: NewOpenAIProvider(apiKey, baseURL, model, temperature),
	}, nil
}

func (p *CustomProvider) ReviseText(ctx context.Context, text, systemPrompt string) (string, error) {
	return p.inner.ReviseText(ctx, text, systemPrompt)
}

func (p *CustomProvider) SetLowReasoning(low bool) {
	if aware, ok := p.inner.(ReasoningAware); ok {
		aware.SetLowReasoning(low)
	}
}

func (p *CustomProvider) ValidateConfig() error {
	return p.inner.ValidateConfig()
}

func (p *CustomProvider) GetName() string {
	return p.name
}

func (p *CustomProvider) GetModel() string {
	return p.inner.GetModel()
}
