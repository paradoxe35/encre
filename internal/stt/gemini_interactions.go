package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/paradoxe35/encre/internal/config"
)

// The Interactions API counts the whole request, base64 included, against 20 MB;
// the Files API lifts that but costs an upload round trip a dictation cannot spare.
const interactionsMaxRequestBytes = 18 * 1024 * 1024

type interactionsRequest struct {
	Model            string               `json:"model"`
	Input            []interactionsInput  `json:"input"`
	GenerationConfig *interactionsGenConf `json:"generation_config,omitempty"`
}

type interactionsInput struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type interactionsGenConf struct {
	TranscriptionConfig *transcriptionConfig `json:"transcription_config,omitempty"`
}

// An empty or absent language_codes is what asks for automatic detection.
type transcriptionConfig struct {
	LanguageCodes []string `json:"language_codes,omitempty"`
}

type interactionsResponse struct {
	Steps []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"steps"`
}

// gemini-3.5-transcribe is a speech model, not a chat model: no prompt, no preamble to strip,
// no thinking budget, and the language is a field instead of a sentence.
func geminiTranscribeSpeech(ctx context.Context, cfg config.SpeechConfig, wav []byte) (string, error) {
	// The -live twin needs the Live API websocket; naming the model that works beats the
	// endpoint's own error.
	if isGeminiLiveModel(cfg.RemoteModel) {
		return "", fmt.Errorf("%s streams over the Live API; use gemini-3.5-transcribe instead",
			cfg.RemoteModel)
	}

	encoded := base64.StdEncoding.EncodeToString(wav)
	if len(encoded) > interactionsMaxRequestBytes {
		return "", fmt.Errorf("recording is too long for Gemini (limit is about %d minutes)",
			interactionsMaxRequestBytes*3/4/(remoteSampleRate*remoteChannels*remoteBitsPerSample/8)/60)
	}

	request := interactionsRequest{
		Model: cfg.RemoteModel,
		Input: []interactionsInput{{Type: "audio", Data: encoded, MimeType: "audio/wav"}},
	}
	if code := MatchLanguage(geminiTranscribeLanguages, cfg.Language); code != "" {
		request.GenerationConfig = &interactionsGenConf{
			TranscriptionConfig: &transcriptionConfig{LanguageCodes: []string{code}},
		}
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiBaseURL(cfg)+"/v1beta/interactions",
		bytes.NewReader(payload))
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
		return "", fmt.Errorf("Gemini returned %s: %s", resp.Status, remoteErrorMessage(body, resp.Status))
	}

	return parseInteractionsResponse(body)
}

func parseInteractionsResponse(body []byte) (string, error) {
	var parsed interactionsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("invalid Gemini response: %w", err)
	}

	var text strings.Builder
	for _, step := range parsed.Steps {
		for _, content := range step.Content {
			if content.Type == "text" {
				text.WriteString(content.Text)
			}
		}
	}

	return strings.TrimSpace(text.String()), nil
}

func isGeminiLiveModel(model string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(model)), "-live")
}
