package ai

import (
	"errors"
	"strings"
	"sync"
)

// ReasoningStyle is how a provider is told to think less. There is no portable parameter: OpenAI
// 400s on reasoning_effort for a non-reasoning model, OpenRouter 400s on both shapes at once.
type ReasoningStyle int

const (
	ReasoningOpenAIEffort ReasoningStyle = iota
	ReasoningOpenRouter
)

// ReasoningAware is a provider with a reasoning parameter to send. Anthropic has none — its
// thinking is opt-in, and a correction does not want it.
type ReasoningAware interface {
	SetLowReasoning(low bool)
}

// DetectReasoningStyle matches by host, not provider type: a custom provider can point at
// OpenRouter too, and it's the one OpenAI-compatible gateway with its own request shape.
func DetectReasoningStyle(baseURL string) ReasoningStyle {
	if strings.Contains(strings.ToLower(baseURL), "openrouter.ai") {
		return ReasoningOpenRouter
	}
	return ReasoningOpenAIEffort
}

// Endpoint/model pairs that refused a reasoning parameter, so the wasted round trip happens once
// per launch. Process-scoped: persisted, a stale rejection could outlive a model upgrade.
var rejected sync.Map

// Retries without the reasoning parameter on a 400 (matched on status: providers word the error
// differently). The rejection is cached only once dropping the parameter fixes it, since a 400
// has other causes.
func withReasoningFallback(endpoint, model string, wanted bool, send func(includeReasoning bool) (string, error)) (string, error) {
	key := endpoint + "::" + model
	if _, refused := rejected.Load(key); !wanted || refused {
		return send(false)
	}

	result, err := send(true)
	if err == nil || !refusedReasoning(err) {
		return result, err
	}

	// If this fails too, the parameter was never the problem and nothing is cached.
	result, retryErr := send(false)
	if retryErr != nil {
		return "", retryErr
	}

	rejected.Store(key, struct{}{})
	return result, nil
}

func refusedReasoning(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == 400 || apiErr.StatusCode == 422
}
