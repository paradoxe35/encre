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

// Gemini counts the whole request, base64 included, against 20 MB. Staying under
// it leaves room for the prompt; at 16 kHz mono that is still about six minutes.
const geminiMaxRequestBytes = 18 * 1024 * 1024

const geminiTranscribeBaseURL = "https://generativelanguage.googleapis.com"

// A chat model will happily answer "Sure, here is the transcript:" unless told
// not to, and that preamble would be typed into the user's document.
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

// Gemini 3 renamed the control from a token budget to a level, and rejects a
// request carrying both. Exactly one field is ever set.
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
	// Set on a thought summary. Nothing here asks for those, so this is a guard
	// against one arriving anyway and being typed out as if it were speech.
	Thought bool `json:"thought"`
}

// thinkingConfigFor asks for the least thinking the model allows. Gemini 3 takes
// a level, where "minimal" is the floor - it cannot be switched off - and older
// models take a zero budget. Sending the wrong one is not rejected, merely
// ignored, so the choice has to be made here rather than left to a retry.
func thinkingConfigFor(model string) *geminiThinkingConfig {
	switch major := geminiMajorVersion(model); {
	case major >= 3:
		return &geminiThinkingConfig{ThinkingLevel: "minimal"}
	case major > 0:
		budget := 0
		return &geminiThinkingConfig{ThinkingBudget: &budget}
	default:
		// An unrecognised name: pay the default thinking rather than guess a
		// parameter and spend a round trip having it refused.
		return nil
	}
}

// geminiMajorVersion reads the 3 out of gemini-3.8-flash and the 2 out of
// gemini-2.5-flash; 0 when the name does not follow the pattern.
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

// geminiTranscribe asks a Gemini model to transcribe wav. Gemini exposes no
// Whisper-style endpoint, so this goes through generateContent, which Google
// still recommends over the newer interactions API for production use.
func geminiTranscribe(ctx context.Context, cfg config.SpeechConfig, wav []byte) (string, error) {
	// A speech model has its own endpoint; the chat models below are the fallback
	// for anyone who points this at a flash model by hand.
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

	// Matched on status, never message text: providers word refusals differently.
	// Remembered only once dropping the field is confirmed to fix it.
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

	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", geminiBaseURL(cfg), cfg.RemoteModel)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", remoteAPIKey(cfg))

	resp, err := remoteClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &remoteStatusError{
			status:  resp.StatusCode,
			message: fmt.Sprintf("Gemini returned %s: %s", resp.Status, remoteErrorMessage(body, resp.Status)),
		}
	}

	return parseGeminiResponse(body)
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

	// Thinking can consume the whole output allowance, which arrives as a normal
	// 200 holding nothing. Reported, because dictating into silence looks like
	// the hotkey failed.
	transcript := strings.TrimSpace(text.String())
	if transcript == "" && candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
		return "", fmt.Errorf("Gemini returned no transcript (%s)", candidate.FinishReason)
	}

	return transcript, nil
}

// remoteStatusError carries the status code so a rejected thinking budget can be
// told apart from a failure worth surfacing.
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
