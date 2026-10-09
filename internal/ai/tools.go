package ai

import (
	"context"
	"encoding/json"
	"sync"
)

// Tool describes a function the model may call, its parameters as a JSON schema object.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
	// signature is Gemini's, which it needs back with the call.
	signature string
}

// Step is one round of tool calls and their results, Results[i] answering Calls[i].
type Step struct {
	Calls   []ToolCall
	Results []string
}

// Reply is a streamed turn: its text, and the tools it calls before it can answer.
type Reply struct {
	Text  string
	Calls []ToolCall
}

// ToolUser is a provider that can offer the model tools.
type ToolUser interface {
	// Turn streams one reply, which ends either in an answer or in tool calls to run first. A model
	// that refuses tools is asked again without them.
	Turn(ctx context.Context, prompt Prompt, onText func(string)) (Reply, error)
}

// arguments is a call's arguments, an empty object when the model gave none.
func (c ToolCall) arguments() json.RawMessage {
	if len(c.Arguments) == 0 {
		return json.RawMessage("{}")
	}
	return c.Arguments
}

// refusedTools holds the endpoint and model pairs that refused tools, so the wasted request happens
// once per launch.
var refusedTools sync.Map

// withToolsFallback sends the prompt, and once more without its tools if the model refuses them. Only
// a first request can show that: once a tool has run, the model has taken tools, and a later 400 has
// another cause.
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
