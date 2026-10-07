package ai

import (
	"context"
	"fmt"
	"sync"
)

// Prompt is one request: the instructions, and the text they apply to.
type Prompt struct {
	System string
	Text   string
}

type Provider interface {
	Name() string
	Model() string
	Complete(ctx context.Context, prompt Prompt) (string, error)
	// Stream hands each piece of the reply to onText as it arrives, and returns the whole of it.
	Stream(ctx context.Context, prompt Prompt, onText func(string)) (string, error)
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
