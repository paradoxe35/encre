package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// APIError keeps the status so callers can react to a 400 without matching provider wording.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

var (
	errNoReply = errors.New("the reply held no text")
	// A thinking model can spend its whole allowance thinking and stop before it writes a word.
	errLengthLimit = errors.New("the reply hit the model's length limit - a model that thinks less, or one with a larger context, would finish")
)

// replyError is the error object the APIs put in a body; Ollama sends a bare string instead.
type replyError struct {
	Message string
}

func (e *replyError) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		e.Message = text
		return nil
	}
	var object struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	e.Message = object.Message
	return nil
}

func (e *replyError) err() error {
	if e == nil {
		return nil
	}
	return &APIError{Message: e.Message}
}

func statusError(provider string, status int, body []byte) error {
	var reply struct {
		Error *replyError `json:"error"`
	}
	message := ""
	if json.Unmarshal(body, &reply) == nil && reply.Error != nil {
		message = reply.Error.Message
	}
	if message == "" {
		message = statusHint(status, body)
	}
	return &APIError{StatusCode: status, Message: fmt.Sprintf("%s: %s (%d)", provider, message, status)}
}

func statusHint(status int, body []byte) string {
	switch status {
	case http.StatusUnauthorized:
		return "the API key was refused"
	case http.StatusForbidden:
		return "the API key lacks access"
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return "no API at this address - check the base URL, which usually ends in /v1"
	case http.StatusTooManyRequests:
		return "rate limited, try again shortly"
	}
	if status >= 500 {
		return "the service is having trouble"
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return preview(text)
	}
	return "request failed"
}

func preview(text string) string {
	const limit = 120
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
