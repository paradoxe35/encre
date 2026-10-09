package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/paradoxe35/encre/internal/config"
)

// Endpoint and model pairs that rejected the quiet request, so the re-upload is paid once.
var geminiThinkingRefused sync.Map

// Gemini caps the request, base64 included, at 20 MB; this leaves room for the prompt (~6 min).
const geminiMaxRequestBytes = 18 * 1024 * 1024

const geminiTranscribeBaseURL = "https://generativelanguage.googleapis.com"

// Otherwise a chat model prefixes "Sure, here is the transcript:", which would be typed out.
const geminiTranscribePrompt = "Transcribe the speech in this audio verbatim. " +
	"Reply with the transcript alone: no preamble, no explanation, no quotes, no markdown. " +
	"If the audio contains no speech, reply with nothing at all."

type geminiRequest struct {
	Contents         []geminiContent    `json:"contents"`
	GenerationConfig *geminiGenerConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiGenerConfig struct {
	Temperature    float64               `json:"temperature"`
	ThinkingConfig *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// Gemini 3 takes a level, older models a budget; a request carrying both is rejected.
type geminiThinkingConfig struct {
	ThinkingLevel  string `json:"thinkingLevel,omitempty"`
	ThinkingBudget *int   `json:"thinkingBudget,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiResponsePart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

type geminiResponsePart struct {
	Text string `json:"text"`
	// Guards against a thought summary being typed out as speech.
	Thought bool `json:"thought"`
}

// The wrong thinking field is ignored, not rejected, so a retry cannot fix it.
func thinkingConfigFor(model string) *geminiThinkingConfig {
	switch major := geminiMajorVersion(model); {
	case major >= 3:
		return &geminiThinkingConfig{ThinkingLevel: "minimal"}
	case major > 0:
		budget := 0
		return &geminiThinkingConfig{ThinkingBudget: &budget}
	default:
		// Unrecognised name: pay default thinking rather than a round trip on a refused parameter.
		return nil
	}
}

// 3 for gemini-3.8-flash, 2 for gemini-2.5-flash, 0 when the name does not follow the pattern.
func geminiMajorVersion(model string) int {
	rest, found := strings.CutPrefix(strings.ToLower(strings.TrimSpace(model)), "gemini-")
	if !found {
		return 0
	}

	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end == 0 {
		return 0
	}
	if end > 0 {
		rest = rest[:end]
	}

	major, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return major
}

// No Whisper-style endpoint; generateContent is what Google recommends for production.
func geminiTranscribe(ctx context.Context, cfg config.SpeechConfig, wav []byte) (string, error) {
	// The chat models below are the fallback for a flash model configured by hand.
	if IsGeminiTranscribeModel(cfg.RemoteModel) {
		return geminiTranscribeSpeech(ctx, cfg, wav)
	}

	encoded := base64.StdEncoding.EncodeToString(wav)
	if len(encoded) > geminiMaxRequestBytes {
		return "", fmt.Errorf("recording is too long for Gemini (limit is about %d minutes)",
			geminiMaxRequestBytes*3/4/(remoteSampleRate*remoteChannels*remoteBitsPerSample/8)/60)
	}

	request := geminiRequest{
		Contents: []geminiContent{{
			Parts: []geminiPart{
				{Text: geminiPromptFor(cfg.Language)},
				{InlineData: &geminiInlineData{MimeType: "audio/wav", Data: encoded}},
			},
		}},
	}

	key := geminiThinkingKey(cfg)
	_, refused := geminiThinkingRefused.Load(key)
	quiet := !refused && thinkingConfigFor(cfg.RemoteModel) != nil

	// Matched on status, not message text; remembered only once dropping the field fixes it.
	text, err := sendGemini(ctx, cfg, request, quiet)
	if quiet && isBadRequest(err) {
		text, err = sendGemini(ctx, cfg, request, false)
		if err == nil {
			geminiThinkingRefused.Store(key, true)
		}
	}
	return text, err
}

func geminiThinkingKey(cfg config.SpeechConfig) string {
	return geminiBaseURL(cfg) + "|" + strings.ToLower(strings.TrimSpace(cfg.RemoteModel))
}

func geminiBaseURL(cfg config.SpeechConfig) string {
	base := strings.TrimRight(cfg.RemoteBaseURL, "/")
	if base == "" {
		return geminiTranscribeBaseURL
	}
	return base
}

func geminiPromptFor(language string) string {
	if language == "" {
		return geminiTranscribePrompt
	}
	return fmt.Sprintf("%s The speech is in %s; write the transcript in that language.",
		geminiTranscribePrompt, LanguageName(language))
}

func sendGemini(ctx context.Context, cfg config.SpeechConfig, request geminiRequest, quiet bool) (string, error) {
	request.GenerationConfig = &geminiGenerConfig{Temperature: 0}
	if quiet {
		request.GenerationConfig.ThinkingConfig = thinkingConfigFor(cfg.RemoteModel)
	}

	body, err := postGemini(ctx, cfg, "/v1beta/models/"+cfg.RemoteModel+":generateContent", request)
	if err != nil {
		return "", err
	}
	return parseGeminiResponse(body)
}

func postGemini(ctx context.Context, cfg config.SpeechConfig, path string, request any) ([]byte, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiBaseURL(cfg)+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", remoteAPIKey(cfg))

	resp, err := remoteClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &remoteStatusError{
			status:  resp.StatusCode,
			message: fmt.Sprintf("Gemini returned %s: %s", resp.Status, remoteErrorMessage(body, resp.Status)),
		}
	}
	return body, nil
}

func parseGeminiResponse(body []byte) (string, error) {
	var parsed geminiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("invalid Gemini response: %w", err)
	}

	if reason := parsed.PromptFeedback.BlockReason; reason != "" {
		return "", fmt.Errorf("Gemini refused the recording (%s)", reason)
	}
	if len(parsed.Candidates) == 0 {
		return "", nil
	}
	candidate := parsed.Candidates[0]

	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		if part.Thought {
			continue
		}
		text.WriteString(part.Text)
	}

	// Thinking can eat the whole output allowance as an empty 200; silence looks like a dead hotkey.
	transcript := strings.TrimSpace(text.String())
	if transcript == "" && candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
		return "", fmt.Errorf("Gemini returned no transcript (%s)", candidate.FinishReason)
	}

	return transcript, nil
}

// Carries the status so a rejected thinking budget can be told apart from a real failure.
type remoteStatusError struct {
	status  int
	message string
}

func (e *remoteStatusError) Error() string { return e.message }

func isBadRequest(err error) bool {
	var statusErr *remoteStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.status == http.StatusBadRequest
}
