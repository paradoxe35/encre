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
	errCutOff      = errors.New("the reply was cut off before it finished - try again")
	errRefused     = errors.New("the model declined to answer")
	errFiltered    = errors.New("the provider's safety filter blocked the reply")
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
	return &APIError{Message: clip(e.Message, messageLimit)}
}

func statusError(provider string, status int, body []byte) error {
	var reply struct {
		Error *replyError `json:"error"`
	}
	message := ""
	if json.Unmarshal(body, &reply) == nil && reply.Error != nil {
		message = clip(reply.Error.Message, messageLimit)
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

func preview(text string) string { return clip(text, 120) }

// A provider's own message is shown whole up to this; proxies put tracebacks there.
const messageLimit = 300

// clip cuts on a rune, so a message in any script ends whole.
func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}
