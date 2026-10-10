package ai

import (
	"context"
	"fmt"
	"sync"
)

// Context travels with the text, not the system prompt, so instructions stay identical per request.
type Prompt struct {
	System  string
	History []Turn
	Text    string
	Context string
	Tools   []Tool
	// ToolUse is appended to System only while Tools are sent, so a retry without them drops it too.
	ToolUse     string
	Steps       []Step
	NoMoreCalls bool
}

func (p Prompt) system() string {
	if len(p.Tools) == 0 || p.ToolUse == "" {
		return p.System
	}
	return p.System + "\n\n" + p.ToolUse
}

type Turn struct {
	Question string
	Answer   string
}

func (p Prompt) conversation(assistant string) []chatMessage {
	messages := make([]chatMessage, 0, 2*len(p.History)+1)
	for _, turn := range p.History {
		messages = append(messages,
			chatMessage{Role: "user", Content: turn.Question},
			chatMessage{Role: assistant, Content: turn.Answer},
		)
	}
	text := p.Text
	if p.Context != "" {
		text += "\n\n" + p.Context
	}
	return append(messages, chatMessage{Role: "user", Content: text})
}

type Provider interface {
	Name() string
	Model() string
	// Stream passes each piece to onText, if given, and returns the whole reply.
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

// Settings changes invalidate the cache so no provider keeps a stale API key or model.
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
