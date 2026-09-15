package stt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

// Gemini 3.1 Pro refuses the "minimal" level, and an older model refuses a level
// outright. Either way the take must still be transcribed rather than lost.
func TestGeminiRetriesWithoutThinkingConfig(t *testing.T) {
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
			io.WriteString(w, `{"error":{"message":"thinkingLevel minimal is not supported"}}`)
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

// Gemini answers with no candidates when the audio holds no speech.
func TestParseGeminiResponseEmpty(t *testing.T) {
	text, err := parseGeminiResponse([]byte(`{"candidates":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}

// Gemini 3 takes a level and Gemini 2.5 a budget; sending both is rejected, and
// sending the wrong one is accepted and ignored, which is worse - it looks like
// it worked while the model thinks at full depth.
func TestThinkingConfigMatchesModelGeneration(t *testing.T) {
	cases := []struct {
		model      string
		wantLevel  string
		wantBudget bool
	}{
		{"gemini-3.8-flash", "minimal", false},
		{"gemini-3.5-flash", "minimal", false},
		{"gemini-3-flash-preview", "minimal", false},
		{"gemini-2.5-flash", "", true},
		{"gemini-2.0-flash-lite", "", true},
	}

	for _, tc := range cases {
		config := thinkingConfigFor(tc.model)
		if config == nil {
			t.Errorf("%s: no thinking config", tc.model)
			continue
		}
		if config.ThinkingLevel != tc.wantLevel {
			t.Errorf("%s: level = %q, want %q", tc.model, config.ThinkingLevel, tc.wantLevel)
		}
		if (config.ThinkingBudget != nil) != tc.wantBudget {
			t.Errorf("%s: budget set = %v, want %v", tc.model, config.ThinkingBudget != nil, tc.wantBudget)
		}
		if config.ThinkingLevel != "" && config.ThinkingBudget != nil {
			t.Errorf("%s: sent both, which Gemini 3 rejects", tc.model)
		}
	}
}

// Guessing a parameter for a name we cannot place costs a refused round trip.
func TestThinkingConfigSkippedForUnknownModel(t *testing.T) {
	for _, model := range []string{"", "gemini-flash-latest", "whisper-1", "gemini"} {
		if config := thinkingConfigFor(model); config != nil {
			t.Errorf("thinkingConfigFor(%q) = %+v, want nil", model, config)
		}
	}
}

func TestGeminiMajorVersion(t *testing.T) {
	cases := map[string]int{
		"gemini-3.8-flash":       3,
		"gemini-3-flash-preview": 3,
		"gemini-2.5-flash":       2,
		"gemini-flash-latest":    0,
		"whisper-1":              0,
		"":                       0,
	}

	for model, want := range cases {
		if got := geminiMajorVersion(model); got != want {
			t.Errorf("geminiMajorVersion(%q) = %d, want %d", model, got, want)
		}
	}
}

// The serialised request must carry one thinking field, never both.
func TestGeminiRequestSendsOneThinkingField(t *testing.T) {
	var sent map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &sent)
		io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	if _, err := RemoteTranscribe(context.Background(), geminiConfig(server.URL), []byte{1, 2}); err != nil {
		t.Fatal(err)
	}

	generation, _ := sent["generationConfig"].(map[string]any)
	thinking, ok := generation["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("no thinkingConfig in %v", generation)
	}
	if _, has := thinking["thinkingBudget"]; has {
		t.Errorf("sent thinkingBudget to a Gemini 3 model: %v", thinking)
	}
	if thinking["thinkingLevel"] != "minimal" {
		t.Errorf("thinkingLevel = %v", thinking["thinkingLevel"])
	}
}

// A blocked or truncated answer is a normal 200 holding no text; dictating into
// silence would otherwise look like the hotkey never fired.
func TestGeminiReportsAnEmptyAnswer(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"blocked prompt", `{"promptFeedback":{"blockReason":"SAFETY"}}`, "SAFETY"},
		{"thinking ate the budget",
			`{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[]}}]}`, "MAX_TOKENS"},
		{"refused answer",
			`{"candidates":[{"finishReason":"SAFETY","content":{"parts":[]}}]}`, "SAFETY"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseGeminiResponse([]byte(tc.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %s", err, tc.want)
			}
		})
	}
}

// Truncated is still better than lost: keep what came back.
func TestGeminiKeepsTruncatedText(t *testing.T) {
	body := `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"half a sen"}]}}]}`

	text, err := parseGeminiResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if text != "half a sen" {
		t.Errorf("text = %q", text)
	}
}

// Nothing asks for thought summaries, but one must never be typed as speech.
func TestGeminiSkipsThoughtParts(t *testing.T) {
	body := `{"candidates":[{"finishReason":"STOP","content":{"parts":[
		{"text":"The user is speaking French.","thought":true},
		{"text":"bonjour"}]}}]}`

	text, err := parseGeminiResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if text != "bonjour" {
		t.Errorf("text = %q, want the thought dropped", text)
	}
}

// gemini-3.5-transcribe is a speech model on its own endpoint: no prompt, and
// the language is a field rather than a sentence.
func TestGeminiTranscribeModelUsesInteractions(t *testing.T) {
	var gotPath string
	var sent interactionsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &sent)
		io.WriteString(w, `{"steps":[{"content":[{"type":"text","text":"  bonjour  "}]}]}`)
	}))
	defer server.Close()

	cfg := geminiConfig(server.URL)
	cfg.RemoteModel = "gemini-3.5-transcribe"
	cfg.Language = "fr"

	text, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}

	if text != "bonjour" {
		t.Errorf("text = %q", text)
	}
	if gotPath != "/v1beta/interactions" {
		t.Errorf("path = %q, want the interactions endpoint", gotPath)
	}
	if sent.Model != "gemini-3.5-transcribe" {
		t.Errorf("model = %q", sent.Model)
	}
	if len(sent.Input) != 1 || sent.Input[0].Type != "audio" || sent.Input[0].MimeType != "audio/wav" {
		t.Errorf("input = %+v", sent.Input)
	}
	if sent.Input[0].Data == "" {
		t.Error("no audio sent")
	}
	// fr must reach the wire as the BCP-47 locale this model takes.
	codes := sent.GenerationConfig.TranscriptionConfig.LanguageCodes
	if len(codes) != 1 || codes[0] != "fr-FR" {
		t.Errorf("language_codes = %v, want [fr-FR]", codes)
	}
}

// An absent language_codes is what asks the model to detect.
func TestGeminiTranscribeAutoDetectSendsNoLanguage(t *testing.T) {
	var sent interactionsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &sent)
		io.WriteString(w, `{"steps":[{"content":[{"type":"text","text":"hello"}]}]}`)
	}))
	defer server.Close()

	cfg := geminiConfig(server.URL)
	cfg.RemoteModel = "gemini-3.5-transcribe"
	cfg.Language = ""

	if _, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if sent.GenerationConfig != nil {
		t.Errorf("sent %+v, want no transcription config", sent.GenerationConfig)
	}
}

// A chat model typed by hand must still work, through generateContent.
func TestGeminiFlashModelStillUsesGenerateContent(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"hi"}]}}]}`)
	}))
	defer server.Close()

	cfg := geminiConfig(server.URL)
	cfg.RemoteModel = "gemini-3.8-flash"

	if _, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, ":generateContent") {
		t.Errorf("path = %q, want generateContent", gotPath)
	}
}

func TestParseInteractionsResponseJoinsTextContent(t *testing.T) {
	body := `{"steps":[{"content":[
		{"type":"text","text":"hello "},
		{"type":"audio","text":"ignored"},
		{"type":"text","text":"world"}]}]}`

	text, err := parseInteractionsResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Errorf("text = %q", text)
	}
}

// The -live twin needs a websocket we do not open; the error should name the
// model that does work rather than relaying a confusing one from the endpoint.
func TestGeminiLiveModelIsRefusedWithAdvice(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	cfg := geminiConfig(server.URL)
	cfg.RemoteModel = "gemini-3.5-transcribe-live"

	_, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2})
	if err == nil {
		t.Fatal("expected an error")
	}
	if called {
		t.Error("sent the audio anyway")
	}
	if !strings.Contains(err.Error(), "gemini-3.5-transcribe") {
		t.Errorf("error = %q, want it to name the working model", err)
	}
}

func TestGeminiUnknownModelDoesNotRetry(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"audio is malformed"}}`)
	}))
	defer server.Close()

	cfg := geminiConfig(server.URL)
	cfg.RemoteModel = "gemini-flash-latest"

	if _, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2}); err == nil {
		t.Fatal("expected the 400 to surface")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want no retry with nothing to drop", attempts)
	}
}

func TestGeminiRemembersARefusedThinkingConfig(t *testing.T) {
	var attempts int
	var sentConfig []bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, _ := io.ReadAll(r.Body)
		var request geminiRequest
		json.Unmarshal(body, &request)
		quiet := request.GenerationConfig != nil && request.GenerationConfig.ThinkingConfig != nil
		sentConfig = append(sentConfig, quiet)

		if quiet {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"thinkingLevel minimal is not supported"}}`)
			return
		}
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	cfg := geminiConfig(server.URL)
	for take := range 2 {
		text, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2})
		if err != nil {
			t.Fatalf("take %d: %v", take, err)
		}
		if text != "ok" {
			t.Fatalf("take %d: text = %q", take, text)
		}
	}

	if attempts != 3 {
		t.Fatalf("attempts = %d, want 2 for the first take and 1 for the second", attempts)
	}
	if want := []bool{true, false, false}; !slices.Equal(sentConfig, want) {
		t.Errorf("thinking config per attempt = %v, want %v", sentConfig, want)
	}
}
