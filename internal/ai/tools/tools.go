// Package tools holds what Ask can look up for the model: free services that need no API key.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/paradoxe35/encre/internal/ai"
)

type Tool struct {
	ai.Tool
	Status func(args json.RawMessage) string
	Run    func(ctx context.Context, args json.RawMessage) (string, error)
}

func Set() []Tool {
	return []Tool{
		webSearch(duckDuckGo(duckDuckGoURL)),
		webPage(guardedClient, renderingReader),
		wikipedia(wikipediaBase),
		weather(geocodingURL, forecastURL),
	}
}

const (
	userAgent = "Encre (+https://github.com/paradoxe35/encre)"
	// maxResult keeps one result to a share of a small model's context.
	maxResult   = 12000
	maxDownload = 2 << 20
)

var client = &http.Client{Timeout: 20 * time.Second}

func get(ctx context.Context, httpClient *http.Client, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	return fetch(httpClient, req)
}

func fetch(httpClient *http.Client, req *http.Request) ([]byte, string, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &statusError{host: req.URL.Host, code: resp.StatusCode, status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload))
	return body, resp.Header.Get("Content-Type"), err
}

type statusError struct {
	host   string
	code   int
	status string
}

func (e *statusError) Error() string { return e.host + " answered " + e.status }

func getJSON(ctx context.Context, url string, into any) error {
	body, _, err := get(ctx, client, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

func arguments[T any](args json.RawMessage) (T, error) {
	var parsed T
	if err := json.Unmarshal(args, &parsed); err != nil {
		return parsed, errors.New("the arguments are not valid JSON")
	}
	return parsed, nil
}

func truncate(text string) string {
	if utf8.RuneCountInString(text) <= maxResult {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxResult]) + "\n[cut short]"
}

func stringParam(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func object(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func aiTool(name, description string, parameters map[string]any) ai.Tool {
	return ai.Tool{Name: name, Description: description, Parameters: parameters}
}
