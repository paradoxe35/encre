package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/paradoxe35/encre/internal/config"
)

type ModelInfo struct {
	ID   string
	Name string
}

// modelsEndpoint is one provider's catalogue: everything is a GET returning a
// list, so only the path and the auth header differ.
type modelsEndpoint struct {
	url     string
	headers map[string]string
}

func endpointFor(provider, apiKey, baseURL string) modelsEndpoint {
	base := strings.TrimRight(baseURL, "/")

	switch provider {
	case config.BuiltInClaude:
		if base == "" {
			base = anthropicBaseURL
		}
		return modelsEndpoint{
			url: base + "/v1/models?limit=1000",
			headers: map[string]string{
				"x-api-key":         apiKey,
				"anthropic-version": anthropicVersion,
			},
		}

	case config.BuiltInGemini:
		if base == "" {
			base = geminiBaseURL
		}
		return modelsEndpoint{
			url:     base + "/v1beta/models?pageSize=1000",
			headers: map[string]string{"x-goog-api-key": apiKey},
		}

	default:
		if base == "" {
			base = openAIBaseURL
		}
		endpoint := modelsEndpoint{url: base + "/models", headers: map[string]string{}}
		// A local OpenAI-compatible server takes no key; an empty Bearer makes
		// some of them reject the call outright.
		if apiKey != "" {
			endpoint.headers["Authorization"] = "Bearer " + apiKey
		}
		return endpoint
	}
}

// Custom providers and anything unrecognised are treated as OpenAI-compatible.
func ListModels(ctx context.Context, provider, apiKey, baseURL string) ([]ModelInfo, error) {
	endpoint := endpointFor(provider, apiKey, baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	for name, value := range endpoint.headers {
		req.Header.Set(name, value)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, truncateString(string(body), 200))
	}

	models, err := decodeModels(resp.Body)
	if err != nil {
		return nil, err
	}

	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// decodeModels reads both list shapes in use: OpenAI and Anthropic answer with
// "data", Gemini with "models" and a "models/" prefix on every id.
func decodeModels(r io.Reader) ([]ModelInfo, error) {
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		Models []struct {
			Name        string   `json:"name"`
			DisplayName string   `json:"displayName"`
			Methods     []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}

	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(payload.Data)+len(payload.Models))

	for _, entry := range payload.Data {
		if entry.ID == "" {
			continue
		}
		name := entry.DisplayName
		if name == "" {
			name = entry.Name
		}
		models = append(models, ModelInfo{ID: entry.ID, Name: name})
	}

	for _, entry := range payload.Models {
		// Gemini lists embedding and media models next to the chat ones.
		if !supportsGeneration(entry.Methods) {
			continue
		}
		models = append(models, ModelInfo{
			ID:   strings.TrimPrefix(entry.Name, "models/"),
			Name: entry.DisplayName,
		})
	}

	return models, nil
}

func supportsGeneration(methods []string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, method := range methods {
		if method == "generateContent" {
			return true
		}
	}
	return false
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
