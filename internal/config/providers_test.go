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

func TestACustomOpenRouterBecomesTheBuiltInAndKeepsItsKey(t *testing.T) {
	cfg := loaded(t, `{
		"ai_provider": {
			"provider": "OpenRouter",
			"providers": {
				"OpenRouter": {"api_key": "k", "base_url": "https://openrouter.ai/api/v1", "model": "openai/gpt-4o-mini", "is_custom": true, "provider_type": "openai-compatible"}
			}
		},
		"operations": {"translate": {"provider_id": "OpenRouter"}}
	}`)

	if _, stale := cfg.AIProvider.Providers["OpenRouter"]; stale {
		t.Fatal("the old spelling is still there")
	}
	settings := cfg.GetProviderSettings(BuiltInOpenRouter)
	if settings.ProviderType != "" {
		t.Fatalf("a built-in kept a custom protocol: %+v", settings)
	}
	if settings.APIKey != "k" || settings.Model != "openai/gpt-4o-mini" {
		t.Fatalf("settings were lost: %+v", settings)
	}
	if cfg.GetCurrentProvider() != BuiltInOpenRouter {
		t.Fatalf("default provider %q was not followed", cfg.GetCurrentProvider())
	}
	if got := cfg.Operation(OpTranslate).ProviderID; got != BuiltInOpenRouter {
		t.Fatalf("the translate override %q was not followed", got)
	}
	if cfg.IsCustomProvider(BuiltInOpenRouter) {
		t.Fatal("reported as custom")
	}
}

// Files from before the flag went away still say is_custom; it is ignored, the name decides.
func TestABuiltInFlaggedCustomByHandIsCorrected(t *testing.T) {
	cfg := loaded(t, `{"ai_provider": {"providers": {"claude": {"api_key": "k", "is_custom": true}}}}`)
	if cfg.IsCustomProvider(BuiltInClaude) {
		t.Fatal("a built-in stayed flagged custom")
	}
	names := cfg.GetAllProviderNames()
	if n := len(names) - len(slices.DeleteFunc(slices.Clone(names), func(s string) bool { return s == BuiltInClaude })); n != 1 {
		t.Fatalf("claude is listed %d times: %v", n, names)
	}
}

func TestACustomProviderWithoutTheFlagIsStillCustom(t *testing.T) {
	cfg := loaded(t, `{"ai_provider": {"providers": {"groq": {"api_key": "k", "base_url": "https://api.groq.com/openai/v1", "model": "m"}}}}`)
	if !cfg.IsCustomProvider("groq") {
		t.Fatal("a hand-written custom provider was not recognised")
	}
	if !slices.Contains(cfg.GetAllProviderNames(), "groq") {
		t.Fatal("the custom provider is missing from the list")
	}
}

func TestAProperBuiltInEntryWinsOverAMisspelledOne(t *testing.T) {
	cfg := loaded(t, `{"ai_provider": {"providers": {
		"openrouter": {"api_key": "proper"},
		"OpenRouter": {"api_key": "stray", "is_custom": true}
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
		"local": {"base_url": "http://localhost:11434/v1", "model": "m", "is_custom": true, "no_api_key": true}
	}}}`)
	if cfg.GetCurrentProvider() != BuiltInGemini || !cfg.IsCustomProvider("local") || cfg.IsCustomProvider(BuiltInGemini) {
		t.Fatalf("a correct config was changed: %+v", cfg.AIProvider)
	}
}
