package ai

import (
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func TestOpenRouterIsTheOpenAIClientUnderItsOwnName(t *testing.T) {
	p := NewOpenRouterProvider("key", "", "google/gemini-2.5-flash", 1)
	if p.Name != config.BuiltInOpenRouter {
		t.Fatalf("name %q", p.Name)
	}
	if p.BaseURL != openRouterBaseURL {
		t.Fatalf("base URL %q, want OpenRouter's", p.BaseURL)
	}
	if DetectReasoningStyle(p.BaseURL) != ReasoningOpenRouter {
		t.Fatal("OpenRouter's own reasoning shape is not selected for its default address")
	}

	custom := NewOpenRouterProvider("key", "https://proxy.example/v1/", "m", 1)
	if custom.BaseURL != "https://proxy.example/v1" {
		t.Fatalf("a configured base URL is not honoured: %q", custom.BaseURL)
	}
}
