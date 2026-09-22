package config

import (
	"encoding/json"
	"slices"
	"testing"
)

func loaded(t *testing.T, raw string) *Config {
	t.Helper()
	cfg := &Config{}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()
	return cfg
}

func TestOpenRouterIsBuiltIn(t *testing.T) {
	if !slices.Contains(BuiltInProviders(), BuiltInOpenRouter) {
		t.Fatal("OpenRouter is missing from the built-ins")
	}
	settings := Default().GetProviderSettings(BuiltInOpenRouter)
	if settings.BaseURL != "https://openrouter.ai/api/v1" || settings.Model == "" {
		t.Fatalf("default OpenRouter settings %+v", settings)
	}
	if err := Default().AddCustomProvider("OpenRouter", ProviderSettings{}); err == nil {
		t.Fatal("a custom provider may not take a built-in name")
	}
}

func TestTheNameDecidesWhetherAProviderIsCustom(t *testing.T) {
	cfg := loaded(t, `{"ai_provider": {"providers": {
		"claude": {"api_key": "k"},
		"groq": {"api_key": "k", "base_url": "https://api.groq.com/openai/v1", "model": "m"}
	}}}`)

	if cfg.IsCustomProvider(BuiltInClaude) {
		t.Fatal("a built-in reads as custom")
	}
	if !cfg.IsCustomProvider("groq") {
		t.Fatal("a provider under its own name does not read as custom")
	}
	if cfg.IsCustomProvider("absent") {
		t.Fatal("a provider that is not configured reads as custom")
	}

	names := cfg.GetAllProviderNames()
	if !slices.Contains(names, "groq") {
		t.Fatalf("the custom provider is missing from %v", names)
	}
	for _, builtIn := range BuiltInProviders() {
		if n := count(names, builtIn); n != 1 {
			t.Fatalf("%s is listed %d times in %v", builtIn, n, names)
		}
	}
}

func TestAProviderAddedByHandUnderABuiltInNameBecomesTheBuiltIn(t *testing.T) {
	cfg := loaded(t, `{
		"ai_provider": {
			"provider": "OpenRouter",
			"providers": {
				"OpenRouter": {"api_key": "k", "base_url": "https://openrouter.ai/api/v1", "model": "openai/gpt-4o-mini", "provider_type": "openai-compatible"}
			}
		},
		"operations": {"translate": {"provider_id": "OpenRouter"}}
	}`)

	if _, stale := cfg.AIProvider.Providers["OpenRouter"]; stale {
		t.Fatal("the old spelling is still there")
	}
	settings := cfg.GetProviderSettings(BuiltInOpenRouter)
	if settings.APIKey != "k" || settings.Model != "openai/gpt-4o-mini" {
		t.Fatalf("settings were lost: %+v", settings)
	}
	if settings.ProviderType != "" {
		t.Fatalf("a built-in kept a custom protocol: %+v", settings)
	}
	if cfg.GetCurrentProvider() != BuiltInOpenRouter {
		t.Fatalf("default provider %q was not followed", cfg.GetCurrentProvider())
	}
	if got := cfg.Operation(OpTranslate).ProviderID; got != BuiltInOpenRouter {
		t.Fatalf("the translate override %q was not followed", got)
	}
}

func TestAProperBuiltInEntryWinsOverAMisspelledOne(t *testing.T) {
	cfg := loaded(t, `{"ai_provider": {"providers": {
		"openrouter": {"api_key": "proper"},
		"OpenRouter": {"api_key": "stray"}
	}}}`)
	if got := cfg.GetProviderSettings(BuiltInOpenRouter).APIKey; got != "proper" {
		t.Fatalf("kept %q, want the entry under the proper name", got)
	}
	if _, stale := cfg.AIProvider.Providers["OpenRouter"]; stale {
		t.Fatal("the stray spelling survived")
	}
}

func TestAWellFormedConfigIsLeftAlone(t *testing.T) {
	cfg := loaded(t, `{"ai_provider": {"provider": "gemini", "providers": {
		"gemini": {"api_key": "g"},
		"local": {"base_url": "http://localhost:11434/v1", "model": "m", "provider_type": "openai-compatible", "no_api_key": true}
	}}}`)
	if cfg.GetCurrentProvider() != BuiltInGemini || !cfg.IsCustomProvider("local") || cfg.IsCustomProvider(BuiltInGemini) {
		t.Fatalf("a correct config was changed: %+v", cfg.AIProvider)
	}
	if cfg.GetProviderSettings("local").ProviderType != ProviderTypeOpenAICompatible {
		t.Fatal("a custom provider lost its protocol")
	}
}

func count(names []string, name string) int {
	n := 0
	for _, candidate := range names {
		if candidate == name {
			n++
		}
	}
	return n
}
