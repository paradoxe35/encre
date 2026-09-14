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
	"strings"

	"github.com/paradoxe35/encre/internal/config"
)

// Gemini counts the whole request, base64 included, against 20 MB. Staying under
// it leaves room for the prompt; at 16 kHz mono that is still about six minutes.
const geminiMaxRequestBytes = 18 * 1024 * 1024

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

type geminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// geminiTranscribe asks a Gemini model to transcribe wav. Gemini exposes no
// Whisper-style endpoint, so this goes through generateContent, which Google
// still recommends over the newer interactions API for production use.
func geminiTranscribe(ctx context.Context, cfg config.SpeechConfig, wav []byte) (string, error) {
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

	// Thinking is latency the user waits through with a half-typed sentence, but
	// a model that cannot switch it off rejects the field outright.
	text, err := sendGemini(ctx, cfg, request, true)
	if isBadRequest(err) {
		return sendGemini(ctx, cfg, request, false)
	}
	return text, err
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
		request.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{ThinkingBudget: 0}
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	base := strings.TrimRight(cfg.RemoteBaseURL, "/")
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", base, cfg.RemoteModel)

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

	var text strings.Builder
	for _, candidate := range parsed.Candidates {
		for _, part := range candidate.Content.Parts {
			text.WriteString(part.Text)
		}
		break
	}

	return strings.TrimSpace(text.String()), nil
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
