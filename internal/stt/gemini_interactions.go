package stt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/paradoxe35/encre/internal/config"
)

// Caps the whole request, base64 included, at 20 MB; the Files API costs a round trip.
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

// A speech model, not chat: no prompt, no thinking budget, and the language is a field.
func geminiTranscribeSpeech(ctx context.Context, cfg config.SpeechConfig, wav []byte) (string, error) {
	// The -live twin needs the Live API websocket; naming the working model beats the endpoint's error.
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

	body, err := postGemini(ctx, cfg, "/v1beta/interactions", request)
	if err != nil {
		return "", err
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
