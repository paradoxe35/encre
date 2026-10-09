package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// protocol is one API's wire format; the provider around it does the HTTP, errors and retries.
type protocol interface {
	request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error)
	decode(body []byte) (string, error)
	// event reads one streamed event into the reply, and reports whether the stream has ended.
	event(data []byte, reply *turn) (done bool, err error)
}

type endpoint struct {
	apiKey      string
	baseURL     string
	model       string
	temperature float64
}

// A redirect would turn the POST into a GET, which an API answers with a puzzling 405.
var httpClient = &http.Client{
	CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		return &redirectError{origin: req.URL.Scheme + "://" + req.URL.Host}
	},
}

type redirectError struct {
	origin string
}

func (e *redirectError) Error() string { return "redirected to " + e.origin }

type provider struct {
	name         string
	protocol     protocol
	endpoint     endpoint
	lowReasoning bool
}

func (p *provider) Name() string  { return p.name }
func (p *provider) Model() string { return p.endpoint.model }

func (p *provider) Complete(ctx context.Context, prompt Prompt) (string, error) {
	return p.withReasoning(func(lowReasoning bool) (string, error) {
		resp, err := p.send(ctx, prompt, false, lowReasoning)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("%s: reading the reply: %w", p.name, err)
		}
		text, err := p.protocol.decode(body)
		if err != nil {
			return "", p.unreadable(err, body)
		}
		return text, nil
	})
}

func (p *provider) Stream(ctx context.Context, prompt Prompt, onText func(string)) (string, error) {
	reply, err := p.stream(ctx, prompt, onText)
	return reply.Text, err
}

func (p *provider) Turn(ctx context.Context, prompt Prompt, onText func(string)) (Reply, error) {
	return withToolsFallback(p.endpoint.baseURL, p.endpoint.model, prompt, func(prompt Prompt) (Reply, error) {
		return p.stream(ctx, prompt, onText)
	})
}

func (p *provider) stream(ctx context.Context, prompt Prompt, onText func(string)) (Reply, error) {
	var reply Reply
	_, err := p.withReasoning(func(lowReasoning bool) (string, error) {
		resp, err := p.send(ctx, prompt, true, lowReasoning)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		streamed := &turn{onText: onText}
		onEvent := activity(ctx)
		err = readEvents(resp.Body, func(data []byte) (bool, error) {
			done, err := p.protocol.event(data, streamed)
			if err == nil {
				onEvent()
			}
			return done, err
		})
		reply = streamed.reply()
		if err != nil {
			return reply.Text, fmt.Errorf("%s: %w", p.name, err)
		}
		return reply.Text, nil
	})
	return reply, err
}

func (p *provider) withReasoning(attempt func(lowReasoning bool) (string, error)) (string, error) {
	return withReasoningFallback(p.endpoint.baseURL, p.endpoint.model, p.lowReasoning, attempt)
}

// send returns the response only once it is known to be a success, so a stream never starts on an error.
func (p *provider) send(ctx context.Context, prompt Prompt, stream, lowReasoning bool) (*http.Response, error) {
	req, err := p.protocol.request(ctx, p.endpoint, prompt, stream, lowReasoning)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if moved, ok := errors.AsType[*redirectError](err); ok {
		return nil, fmt.Errorf("%s: the address redirects to %s - use that in the base URL", p.name, moved.origin)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, statusError(p.name, resp.StatusCode, body)
	}
	return resp, nil
}

func (p *provider) unreadable(err error, body []byte) error {
	text := strings.TrimSpace(string(body))
	switch {
	case text == "":
		return fmt.Errorf("%s: empty reply - check the base URL", p.name)
	case strings.HasPrefix(text, "<"):
		return fmt.Errorf("%s: got a web page instead of the API - check the base URL", p.name)
	default:
		return fmt.Errorf("%s: unexpected reply (%v): %s", p.name, err, preview(text))
	}
}

func newJSONRequest(ctx context.Context, url string, body any, headers map[string]string) (*http.Request, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return req, nil
}
