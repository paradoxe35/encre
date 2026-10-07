package ai

import (
	"errors"
	"sync"
)

// Endpoint/model pairs that refused a reasoning parameter, so the wasted round trip happens once
// per launch. Process-scoped: persisted, a stale rejection could outlive a model upgrade.
var rejected sync.Map

// Retries without the reasoning parameter on a 400 (matched on status: providers word the error
// differently). The rejection is cached only once dropping the parameter fixes it, since a 400
// has other causes.
func withReasoningFallback(endpoint, model string, wanted bool, send func(lowReasoning bool) (string, error)) (string, error) {
	key := endpoint + "::" + model
	if _, refused := rejected.Load(key); !wanted || refused {
		return send(false)
	}

	result, err := send(true)
	if err == nil || !refusedReasoning(err) {
		return result, err
	}

	result, retryErr := send(false)
	if retryErr != nil {
		return "", retryErr
	}

	rejected.Store(key, struct{}{})
	return result, nil
}

func refusedReasoning(err error) bool {
	apiErr, ok := errors.AsType[*APIError](err)
	return ok && (apiErr.StatusCode == 400 || apiErr.StatusCode == 422)
}
