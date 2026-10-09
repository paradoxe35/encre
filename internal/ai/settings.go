package ai

import (
	"cmp"
	"fmt"
	"net/url"
	"strings"

	"github.com/paradoxe35/encre/internal/config"
)

type builtIn struct {
	protocol protocol
	baseURL  string
	model    string
}

var builtIns = map[string]builtIn{
	config.BuiltInOpenAI:     {chatCompletions{}, "https://api.openai.com/v1", "gpt-6-luna"},
	config.BuiltInClaude:     {messages{}, "https://api.anthropic.com", "claude-haiku-4-5"},
	config.BuiltInGemini:     {generateContent{}, "https://generativelanguage.googleapis.com", "gemini-3.1-flash-lite"},
	config.BuiltInOpenRouter: {chatCompletions{}, "https://openrouter.ai/api/v1", "openai/gpt-6-luna"},
}

const defaultTemperature = 1.0

// A custom provider speaks the OpenAI protocol.
func FromSettings(name string, settings config.ProviderSettings, apiKey string, custom bool) (Provider, error) {
	target := endpoint{
		apiKey:      apiKey,
		baseURL:     strings.TrimSpace(settings.BaseURL),
		model:       strings.TrimSpace(settings.Model),
		temperature: cmp.Or(settings.Temperature, defaultTemperature),
	}

	var wire protocol
	if custom {
		if settings.ProviderType != "" && settings.ProviderType != config.ProviderTypeOpenAICompatible {
			return nil, fmt.Errorf("unsupported custom provider type: %s", settings.ProviderType)
		}
		if target.baseURL == "" {
			return nil, fmt.Errorf("base URL is required for custom providers")
		}
		wire = chatCompletions{}
		target.baseURL = withAPIVersion(target.baseURL)
	} else {
		known, ok := builtIns[name]
		if !ok {
			return nil, fmt.Errorf("unknown provider: %s", name)
		}
		wire = known.protocol
		target.baseURL = cmp.Or(target.baseURL, known.baseURL)
		target.model = cmp.Or(target.model, known.model)
	}
	target.baseURL = strings.TrimRight(target.baseURL, "/")

	return &provider{
		name:         name,
		protocol:     wire,
		endpoint:     target,
		lowReasoning: settings.LowReasoning,
	}, nil
}

// Local OpenAI-compatible servers (Ollama, LM Studio, llama.cpp, vLLM) all serve under /v1.
func withAPIVersion(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || strings.Trim(parsed.Path, "/") != "" {
		return baseURL
	}
	parsed.Path = "/v1"
	return parsed.String()
}

func defaultBaseURL(provider string) string {
	if known, ok := builtIns[provider]; ok {
		return known.baseURL
	}
	return builtIns[config.BuiltInOpenAI].baseURL
}
