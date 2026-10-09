package ai

import (
	"errors"
	"sync"
)

// Not persisted: a stale rejection could outlive a model upgrade.
var rejected sync.Map

// A 400 has other causes, so the rejection is cached only once the retry succeeds.
func withReasoningFallback(endpoint, model string, wanted bool, send func(lowReasoning bool) (string, error)) (string, error) {
	key := endpoint + "::" + model
	if _, refused := rejected.Load(key); !wanted || refused {
		return send(false)
	}

	result, err := send(true)
	if err == nil || !refusedRequest(err) {
		return result, err
	}

	result, retryErr := send(false)
	if retryErr != nil {
		return "", retryErr
	}

	rejected.Store(key, struct{}{})
	return result, nil
}

func refusedRequest(err error) bool {
	apiErr, ok := errors.AsType[*APIError](err)
	return ok && (apiErr.StatusCode == 400 || apiErr.StatusCode == 422)
}
