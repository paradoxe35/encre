package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// APIError carries the status alongside the message, so a caller can react to a 400 without
// matching on wording that every provider spells differently.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

func apiError(statusCode int, format string, args ...any) error {
	return &APIError{StatusCode: statusCode, Message: fmt.Sprintf(format, args...)}
}

func ParseAPIError(statusCode int, body []byte, providerName string) error {
	var errResp struct {
		Error *struct {
			Message string      `json:"message"`
			Type    string      `json:"type"`
			Code    interface{} `json:"code,omitempty"`
			Status  string      `json:"status,omitempty"`
		} `json:"error,omitempty"`
	}

	if json.Unmarshal(body, &errResp) == nil && errResp.Error != nil {
		return apiError(statusCode, "%s API error (%d): %s", providerName, statusCode, errResp.Error.Message)
	}

	switch statusCode {
	case 401:
		return apiError(statusCode, "%s authentication failed: invalid API key or credentials", providerName)
	case 403:
		return apiError(statusCode, "%s access forbidden: check your API key permissions", providerName)
	case 404:
		return apiError(statusCode, "%s endpoint not found: verify the base URL is correct", providerName)
	case 429:
		return apiError(statusCode, "%s rate limit exceeded: please try again later", providerName)
	case 500, 502, 503, 504:
		return apiError(statusCode, "%s API server error (%d): service may be temporarily unavailable", providerName, statusCode)
	default:
		preview := string(body)
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		if len(preview) > 0 {
			return apiError(statusCode, "%s API request failed (%d): %s", providerName, statusCode, preview)
		}
		return apiError(statusCode, "%s API request failed with status code %d", providerName, statusCode)
	}
}

func ParseUnmarshalError(err error, body []byte, statusCode int, providerName string) error {
	bodyPreview := string(body)
	if len(bodyPreview) > 200 {
		bodyPreview = bodyPreview[:200] + "..."
	}

	if len(body) == 0 {
		return fmt.Errorf("received empty response from API: verify the base URL is correct")
	}

	if strings.Contains(string(body), "<!DOCTYPE") || strings.Contains(string(body), "<html") {
		return fmt.Errorf("received HTML instead of JSON: verify the base URL points to the API endpoint")
	}

	if statusCode == 404 {
		var exampleURL string
		switch providerName {
		case "openai":
			exampleURL = "https://api.openai.com/v1"
		case "claude":
			exampleURL = "https://api.anthropic.com"
		case "gemini":
			exampleURL = "https://generativelanguage.googleapis.com"
		}
		if exampleURL != "" {
			return fmt.Errorf("endpoint not found: verify the base URL is correct (e.g., %s)", exampleURL)
		}
		return fmt.Errorf("endpoint not found: verify the base URL is correct")
	}

	return fmt.Errorf("invalid API response format: %v (status: %d, response: %s)", err, statusCode, bodyPreview)
}
