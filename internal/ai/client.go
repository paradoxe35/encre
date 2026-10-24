package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// protocol is one API's wire format; the provider around it does the HTTP, errors and retries.
type protocol interface {
	request(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Request, error)
	// decode reads a reply sent whole, by a server asked not to stream or that ignores the request to.
	decode(body []byte) (string, error)
	// event reads one streamed event into the reply, and reports whether the stream has ended.
	event(data []byte, reply *turn) (done bool, err error)
}

type endpoint struct {
	apiKey      string
	baseURL     string
	model       string
	temperature float64
	// maxTokens is a model's output limit learned from a refusal; 0 leaves the protocol's own.
	maxTokens int
}

// A redirect would turn the POST into a GET, which an API answers with a puzzling 405.
// Every caller bounds its own wait, the actions by the silence timeout set in Settings.
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
		resp, err := p.open(ctx, prompt, lowReasoning)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		streamed := &turn{onText: onText}
		onEvent := activity(ctx)
		events := 0
		head := &prefix{room: maxEventSize}
		err = readEvents(io.TeeReader(resp.Body, head), func(data []byte) (bool, error) {
			done, err := p.protocol.event(data, streamed)
			if err == nil {
				events++
				onEvent()
			}
			streamed.ended = streamed.ended || done
			return done, err
		})
		if err == nil && events > 0 && !streamed.ended {
			err = errCutOff
		}
		if err == nil && events == 0 {
			text, err := p.protocol.decode(head.Bytes())
			if err != nil {
				return "", p.unreadable(err, head.Bytes())
			}
			streamed.write(text)
		}
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

// Endpoint/model pairs that refused to stream, asked for whole replies until the app restarts.
var refusedStreaming sync.Map

func (p *provider) open(ctx context.Context, prompt Prompt, lowReasoning bool) (*http.Response, error) {
	key := p.endpoint.baseURL + "::" + p.endpoint.model
	if _, refused := refusedStreaming.Load(key); refused {
		return p.send(ctx, prompt, false, lowReasoning)
	}
	resp, err := p.send(ctx, prompt, true, lowReasoning)
	if !streamingRefused(err) {
		return resp, err
	}
	resp, err = p.send(ctx, prompt, false, lowReasoning)
	if err == nil {
		refusedStreaming.Store(key, struct{}{})
	}
	return resp, err
}

// Matched on wording too: a 400 has many causes, and only this one is cured by not streaming.
func streamingRefused(err error) bool {
	return refusedRequest(err) && strings.Contains(strings.ToLower(err.Error()), "stream")
}

// Endpoint/model pairs whose 400 named a setting they refuse, sent without it until the app restarts.
var relaxedEndpoints sync.Map

// What a refusal taught about an endpoint; the key and settings stay the provider's own.
type relaxation struct {
	dropTemperature bool
	maxTokens       int
}

func (r relaxation) apply(target endpoint) endpoint {
	if r.dropTemperature {
		target.temperature = defaultTemperature
	}
	if r.maxTokens > 0 && (target.maxTokens == 0 || r.maxTokens < target.maxTokens) {
		target.maxTokens = r.maxTokens
	}
	return target
}

// A refused setting is retried once without it, and remembered only once the retry succeeds.
func (p *provider) send(ctx context.Context, prompt Prompt, stream, lowReasoning bool) (*http.Response, error) {
	key := p.endpoint.baseURL + "::" + p.endpoint.model
	var known relaxation
	if learned, ok := relaxedEndpoints.Load(key); ok {
		known = learned.(relaxation)
	}
	target := known.apply(p.endpoint)
	resp, err := p.post(ctx, target, prompt, stream, lowReasoning)
	learned, ok := relaxed(target, known, err)
	if !ok {
		return resp, err
	}
	resp, err = p.post(ctx, learned.apply(p.endpoint), prompt, stream, lowReasoning)
	if err == nil {
		relaxedEndpoints.Store(key, learned)
	}
	return resp, err
}

var outputLimit = regexp.MustCompile(`max_tokens: \d+ > (\d+)`)

// Current Claude models refuse any temperature, and older ones a max_tokens above their output limit.
func relaxed(target endpoint, known relaxation, err error) (relaxation, bool) {
	if !refusedRequest(err) {
		return known, false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "temperature") && target.temperature != defaultTemperature {
		known.dropTemperature = true
		return known, true
	}
	if match := outputLimit.FindStringSubmatch(message); match != nil {
		limit, _ := strconv.Atoi(match[1])
		if limit > 0 && (target.maxTokens == 0 || limit < target.maxTokens) {
			known.maxTokens = limit
			return known, true
		}
	}
	return known, false
}

// Returns only a successful response, so a stream never starts on an error.
func (p *provider) post(ctx context.Context, target endpoint, prompt Prompt, stream, lowReasoning bool) (*http.Response, error) {
	req, err := p.protocol.request(ctx, target, prompt, stream, lowReasoning)
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
	case errors.Is(err, errLengthLimit), errors.Is(err, errRefused), errors.Is(err, errFiltered):
		return fmt.Errorf("%s: %w", p.name, err)
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
