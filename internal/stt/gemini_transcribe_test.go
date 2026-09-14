package stt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func geminiConfig(baseURL string) config.SpeechConfig {
	return config.SpeechConfig{
		Engine:         config.SpeechRemote,
		RemoteProvider: "gemini",
		RemoteModel:    "gemini-3.8-flash",
		RemoteBaseURL:  baseURL,
		RemoteAPIKey:   "test-key",
	}
}

func TestProtocolForPresets(t *testing.T) {
	cases := map[string]RemoteProtocol{
		"openai":  ProtocolOpenAI,
		"groq":    ProtocolOpenAI,
		"gemini":  ProtocolGemini,
		"custom":  ProtocolOpenAI,
		"unknown": ProtocolOpenAI,
	}

	for id, want := range cases {
		if got := protocolFor(id); got != want {
			t.Errorf("protocolFor(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestGeminiTranscribeRequest(t *testing.T) {
	var gotPath, gotKey, gotMime string
	var gotAudio []byte
	var gotPrompt string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")

		body, _ := io.ReadAll(r.Body)
		var request geminiRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}

		for _, part := range request.Contents[0].Parts {
			if part.InlineData != nil {
				gotMime = part.InlineData.MimeType
				gotAudio, _ = base64.StdEncoding.DecodeString(part.InlineData.Data)
			}
			if part.Text != "" {
				gotPrompt = part.Text
			}
		}

		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"  bonjour le monde  "}]}}]}`)
	}))
	defer server.Close()

	pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	text, err := RemoteTranscribe(context.Background(), geminiConfig(server.URL), pcm)
	if err != nil {
		t.Fatal(err)
	}

	if text != "bonjour le monde" {
		t.Errorf("text = %q, want the transcript trimmed", text)
	}
	if gotPath != "/v1beta/models/gemini-3.8-flash:generateContent" {
		t.Errorf("path = %q", gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("x-goog-api-key = %q", gotKey)
	}
	if gotMime != "audio/wav" {
		t.Errorf("mime = %q", gotMime)
	}
	// The audio must arrive as a WAV, not the headerless PCM StopPCM returns.
	if len(gotAudio) != len(pcm)+44 || string(gotAudio[0:4]) != "RIFF" {
		t.Errorf("audio is not a WAV: %d bytes, starts %q", len(gotAudio), gotAudio[:4])
	}
	if !strings.Contains(gotPrompt, "verbatim") {
		t.Errorf("prompt = %q", gotPrompt)
	}
}

// A model that cannot switch thinking off rejects thinkingBudget; the take must
// still be transcribed rather than lost.
func TestGeminiRetriesWithoutThinkingBudget(t *testing.T) {
	var attempts int
	var sentBudget []bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++

		body, _ := io.ReadAll(r.Body)
		var request geminiRequest
		json.Unmarshal(body, &request)
		sentBudget = append(sentBudget,
			request.GenerationConfig != nil && request.GenerationConfig.ThinkingConfig != nil)

		if attempts == 1 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"thinkingBudget is not supported"}}`)
			return
		}
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	text, err := RemoteTranscribe(context.Background(), geminiConfig(server.URL), []byte{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if text != "ok" {
		t.Errorf("text = %q", text)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want a retry", attempts)
	}
	if !sentBudget[0] || sentBudget[1] {
		t.Errorf("budget sent per attempt = %v, want [true false]", sentBudget)
	}
}

func TestGeminiSurfacesOtherErrors(t *testing.T) {
	var attempts int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"API key not valid"}}`)
	}))
	defer server.Close()

	_, err := RemoteTranscribe(context.Background(), geminiConfig(server.URL), []byte{1, 2})
	if err == nil {
		t.Fatal("expected an error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want no retry on 401", attempts)
	}
	if !strings.Contains(err.Error(), "API key not valid") {
		t.Errorf("error = %q, want the provider message", err)
	}
}

func TestGeminiPromptNamesTheLanguage(t *testing.T) {
	if strings.Contains(geminiPromptFor(""), "The speech is in") {
		t.Error("auto-detect must not name a language")
	}

	prompt := geminiPromptFor("fr")
	if !strings.Contains(prompt, "French") {
		t.Errorf("prompt = %q, want the language name rather than the code", prompt)
	}
}

func TestParseGeminiResponseJoinsParts(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"text":"hello "},{"text":"world"}]}}]}`

	text, err := parseGeminiResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Errorf("text = %q", text)
	}
}

// Gemini answers with no parts when the audio holds no speech.
func TestParseGeminiResponseEmpty(t *testing.T) {
	text, err := parseGeminiResponse([]byte(`{"candidates":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}
