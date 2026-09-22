package ai

import (
	"strings"
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func TestDecodeModelsOpenAI(t *testing.T) {
	body := `{"object":"list","data":[{"id":"gpt-4o","object":"model","created":1715367049}]}`

	models, err := decodeModels(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-4o" {
		t.Fatalf("got %+v", models)
	}
}

func TestDecodeModelsAnthropic(t *testing.T) {
	body := `{"data":[{"type":"model","id":"claude-opus-5","display_name":"Claude Opus 5",
		"created_at":"2026-07-24T00:00:00Z"}],"has_more":false}`

	models, err := decodeModels(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models", len(models))
	}
	if models[0].ID != "claude-opus-5" || models[0].Name != "Claude Opus 5" {
		t.Fatalf("got %+v", models[0])
	}
}

// Gemini prefixes every id and lists models that cannot answer a prompt.
func TestDecodeModelsGemini(t *testing.T) {
	body := `{"models":[
		{"name":"models/gemini-3.8-flash","displayName":"Gemini 3.8 Flash",
		 "supportedGenerationMethods":["generateContent","countTokens"]},
		{"name":"models/text-embedding-004","displayName":"Embedding 004",
		 "supportedGenerationMethods":["embedContent"]}]}`

	models, err := decodeModels(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("expected the embedding model to be dropped, got %+v", models)
	}
	if models[0].ID != "gemini-3.8-flash" {
		t.Fatalf("prefix not stripped: %q", models[0].ID)
	}
}

func TestEndpointForBuiltIns(t *testing.T) {
	cases := []struct {
		provider string
		baseURL  string
		wantURL  string
		wantKey  string
	}{
		{config.BuiltInOpenAI, "", openAIBaseURL + "/models", "Authorization"},
		{config.BuiltInClaude, "", anthropicBaseURL + "/v1/models?limit=1000", "x-api-key"},
		{config.BuiltInGemini, "", geminiBaseURL + "/v1beta/models?pageSize=1000", "x-goog-api-key"},
		{config.BuiltInOpenRouter, "", openRouterBaseURL + "/models", "Authorization"},
		{"my-ollama", "http://localhost:11434/v1/", "http://localhost:11434/v1/models", "Authorization"},
	}

	for _, tc := range cases {
		endpoint := endpointFor(tc.provider, "secret", tc.baseURL)
		if endpoint.url != tc.wantURL {
			t.Errorf("%s: url = %q, want %q", tc.provider, endpoint.url, tc.wantURL)
		}
		if _, ok := endpoint.headers[tc.wantKey]; !ok {
			t.Errorf("%s: missing %s header, got %v", tc.provider, tc.wantKey, endpoint.headers)
		}
	}
}

// A local server takes no key, and some reject a bare "Bearer " outright.
func TestEndpointOmitsEmptyBearer(t *testing.T) {
	endpoint := endpointFor("my-llama", "", "http://localhost:8080/v1")
	if _, ok := endpoint.headers["Authorization"]; ok {
		t.Fatalf("sent an Authorization header with no key: %v", endpoint.headers)
	}
}

func TestAnthropicVersionMatchesMessagesCall(t *testing.T) {
	endpoint := endpointFor(config.BuiltInClaude, "secret", "")
	if endpoint.headers["anthropic-version"] != anthropicVersion {
		t.Fatalf("version header = %q", endpoint.headers["anthropic-version"])
	}
}
