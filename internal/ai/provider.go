package ai

import (
	"context"
	"fmt"
	"sync"
)

type Provider interface {
	ReviseText(ctx context.Context, text, systemPrompt string) (string, error)
	ValidateConfig() error
	GetName() string
	GetModel() string
}

type ProviderFactory struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewProviderFactory() *ProviderFactory {
	return &ProviderFactory{
		providers: make(map[string]Provider),
	}
}

func (f *ProviderFactory) Register(name string, provider Provider) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers[name] = provider
}

// Settings changes invalidate every cached provider; a stale entry would keep an
// outdated API key or model.
func (f *ProviderFactory) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers = make(map[string]Provider)
}

func (f *ProviderFactory) Get(name string) (Provider, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	provider, ok := f.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %s not found", name)
	}
	return provider, nil
}

func (f *ProviderFactory) SetCurrent(name string) error {
	provider, err := f.Get(name)
	if err != nil {
		return err
	}
	if err := provider.ValidateConfig(); err != nil {
		return fmt.Errorf("provider validation failed: %w", err)
	}
	return nil
}
