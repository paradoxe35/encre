package ai

import (
	"context"
	"encoding/json"
	"sync"
)

type Tool struct {
	Name        string
	Description string
	// Parameters is a JSON schema object.
	Parameters map[string]any
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
	// signature is Gemini's, which it needs back with the call.
	signature string
}

// Step is one round of tool calls; Results[i] answers Calls[i].
type Step struct {
	Calls   []ToolCall
	Results []string
}

type Reply struct {
	Text  string
	Calls []ToolCall
}

type ToolUser interface {
	// Turn streams one reply. A model that refuses tools is asked again without them.
	Turn(ctx context.Context, prompt Prompt, onText func(string)) (Reply, error)
}

func (c ToolCall) arguments() json.RawMessage {
	if len(c.Arguments) == 0 {
		return json.RawMessage("{}")
	}
	return c.Arguments
}

// refusedTools remembers the models that refused tools, so the wasted request happens once per launch.
var refusedTools sync.Map

// withToolsFallback retries without tools only on a first request: once a tool has run, the model has
// taken tools, and a later refusal has another cause.
func withToolsFallback(endpoint, model string, prompt Prompt, send func(Prompt) (Reply, error)) (Reply, error) {
	key := endpoint + "::" + model
	if _, refused := refusedTools.Load(key); refused {
		prompt.Tools = nil
	}
	if len(prompt.Tools) == 0 || len(prompt.Steps) > 0 {
		return send(prompt)
	}

	reply, err := send(prompt)
	if err == nil || !refusedRequest(err) {
		return reply, err
	}
	prompt.Tools = nil
	reply, retryErr := send(prompt)
	if retryErr != nil {
		return Reply{}, retryErr
	}
	refusedTools.Store(key, struct{}{})
	return reply, nil
}
