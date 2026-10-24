package stt

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func TestWavHeaderFields(t *testing.T) {
	pcm := make([]byte, 1000)
	wav := wavFile(pcm)

	if len(wav) != 44+len(pcm) {
		t.Fatalf("got length %d, want %d", len(wav), 44+len(pcm))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatalf("missing RIFF/WAVE markers: %q", wav[0:12])
	}
	if string(wav[12:16]) != "fmt " || string(wav[36:40]) != "data" {
		t.Fatalf("missing fmt /data markers: %q %q", wav[12:16], wav[36:40])
	}

	if got := binary.LittleEndian.Uint32(wav[4:8]); got != uint32(36+len(pcm)) {
		t.Errorf("ChunkSize: got %d, want %d", got, 36+len(pcm))
	}
	if got := binary.LittleEndian.Uint16(wav[20:22]); got != 1 {
		t.Errorf("AudioFormat: got %d, want PCM(1)", got)
	}
	if got := binary.LittleEndian.Uint16(wav[22:24]); got != 1 {
		t.Errorf("NumChannels: got %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint32(wav[24:28]); got != 16000 {
		t.Errorf("SampleRate: got %d, want 16000", got)
	}
	if got := binary.LittleEndian.Uint32(wav[28:32]); got != 32000 {
		t.Errorf("ByteRate: got %d, want 32000", got)
	}
	if got := binary.LittleEndian.Uint16(wav[32:34]); got != 2 {
		t.Errorf("BlockAlign: got %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(wav[34:36]); got != 16 {
		t.Errorf("BitsPerSample: got %d, want 16", got)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != uint32(len(pcm)) {
		t.Errorf("Subchunk2Size: got %d, want %d", got, len(pcm))
	}
}

func TestRemoteLanguageField(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"gpt-transcribe", "languages[]"},
		{"gpt-4o-transcribe", "language"},
		{"gpt-4o-mini-transcribe", "language"},
		{"whisper-1", "language"},
		{"whisper-large-v3-turbo", "language"},
		{"whisper-large-v3", "language"},
		{"some-custom-model", "language"},
		{"", "language"},
	}

	for _, c := range cases {
		if got := remoteLanguageField(c.model); got != c.want {
			t.Errorf("remoteLanguageField(%q): got %q, want %q", c.model, got, c.want)
		}
	}
}

func TestParseRemoteResponseText(t *testing.T) {
	text, err := parseRemoteResponse([]byte(`{"text":"hello world"}`))
	if err != nil || text != "hello world" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestParseRemoteResponseInvalidJSON(t *testing.T) {
	if _, err := parseRemoteResponse([]byte(`not json`)); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRemoteErrorMessageNestedObject(t *testing.T) {
	got := remoteErrorMessage([]byte(`{"error":{"message":"invalid api key"}}`), "401 Unauthorized")
	if got != "invalid api key" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoteErrorMessageFlatString(t *testing.T) {
	got := remoteErrorMessage([]byte(`{"error":"bad request"}`), "400 Bad Request")
	if got != "bad request" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoteErrorMessageFallsBackToStatus(t *testing.T) {
	got := remoteErrorMessage([]byte(``), "500 Internal Server Error")
	if got != "500 Internal Server Error" {
		t.Fatalf("got %q", got)
	}
}

// A take over the upload cap is sent in pieces under it, and a failed piece keeps the text before it.
func TestALongTakeIsSentInPiecesUnderTheLimit(t *testing.T) {
	for name, failAt := range map[string]int{"all pieces": 0, "second piece fails": 2} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			pieces := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				file, _, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				size, _ := io.Copy(io.Discard, file)
				mu.Lock()
				pieces++
				piece := pieces
				mu.Unlock()
				if size > remoteMaxAudioBytes {
					t.Errorf("piece %d is %d bytes, over the %d limit", piece, size, remoteMaxAudioBytes)
				}
				if size > remoteUploadSafeBytes {
					t.Errorf("piece %d is %d bytes, over the safe %d", piece, size, remoteUploadSafeBytes)
				}
				if piece == failAt {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				fmt.Fprintf(w, `{"text":"part %d"}`, piece)
			}))
			defer server.Close()

			cfg := config.SpeechConfig{RemoteBaseURL: server.URL, RemoteModel: "whisper-1"}
			text, err := RemoteTranscribe(context.Background(), cfg, make([]byte, 2*remoteUploadSafeBytes+1))
			if failAt == 0 {
				if err != nil || text != "part 1 part 2 part 3" {
					t.Fatalf("got %q, %v; want every piece joined", text, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "part 2 of 3") || text != "part 1 part 3" {
				t.Fatalf("got %q, %v; want the other pieces' text and the second's error", text, err)
			}
		})
	}
}

func TestRemoteAPIKeyFallsBackToPlaintext(t *testing.T) {
	for _, key := range []string{"sk-plainkey-not-encrypted", "gsk_abc123", "AIzaSyA1234567890abcdefghijklmnopqrstu"} {
		got, err := remoteAPIKey(config.SpeechConfig{RemoteAPIKey: key})
		if err != nil || got != key {
			t.Errorf("%q: got %q, %v", key, got, err)
		}
	}
}

// Sent as is, a key encrypted on another machine only earns a puzzling 401.
func TestRemoteAPIKeyThatFailsToDecryptIsReported(t *testing.T) {
	foreign := base64.StdEncoding.EncodeToString(make([]byte, 48))
	if _, err := remoteAPIKey(config.SpeechConfig{RemoteAPIKey: foreign}); err == nil || !strings.Contains(err.Error(), "re-enter it in Settings") {
		t.Fatalf("got %v, want the advice to re-enter the key", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	cfg := config.SpeechConfig{RemoteBaseURL: server.URL, RemoteModel: "whisper-1", RemoteAPIKey: foreign}
	if _, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2}); err == nil || requests != 0 {
		t.Fatalf("got %v after %d requests, want an error before any request", err, requests)
	}
}

func TestRemoteErrorPageIsCut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>"+strings.Repeat("x", 1<<20)+"</html>")
	}))
	defer server.Close()

	cfg := config.SpeechConfig{RemoteBaseURL: server.URL, RemoteModel: "whisper-1"}
	_, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2})
	if err == nil || len(err.Error()) > 500 {
		t.Fatalf("got an error of %d bytes", len(err.Error()))
	}
}

func TestRemoteResponseIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"text":"`+strings.Repeat("x", remoteMaxResponseBytes)+`"}`)
	}))
	defer server.Close()

	cfg := config.SpeechConfig{RemoteBaseURL: server.URL, RemoteModel: "whisper-1"}
	if _, err := RemoteTranscribe(context.Background(), cfg, []byte{1, 2}); err == nil {
		t.Fatal("an oversized response was accepted")
	}
}

func TestRemoteAPIKeyDecrypts(t *testing.T) {
	encrypted, err := config.EncryptAPIKey("sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.SpeechConfig{RemoteAPIKey: encrypted}
	if got, err := remoteAPIKey(cfg); err != nil || got != "sk-secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNothingHeardIsNeverSentToTheService(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"text":"invented"}`))
	}))
	defer server.Close()

	for _, provider := range []string{"custom", geminiPresetID(t)} {
		cfg := config.SpeechConfig{RemoteProvider: provider, RemoteBaseURL: server.URL, RemoteModel: "m", RemoteAPIKey: "k"}
		text, err := RemoteTranscribe(context.Background(), cfg, nil)
		if err != nil || text != "" {
			t.Fatalf("%s: an empty take gave %q, %v", provider, text, err)
		}
	}
	if requests != 0 {
		t.Fatalf("an empty take was sent %d times", requests)
	}
}

func geminiPresetID(t *testing.T) string {
	t.Helper()
	for _, preset := range RemotePresets {
		if preset.Protocol == ProtocolGemini {
			return preset.ID
		}
	}
	t.Skip("no Gemini preset")
	return ""
}

func TestAFullGeminiPieceFitsTheRequestOnceEncoded(t *testing.T) {
	for model, limit := range map[string]int{"gemini-2.5-flash": geminiMaxRequestBytes, "gemini-3.5-transcribe": interactionsMaxRequestBytes} {
		cfg := config.SpeechConfig{RemoteProvider: geminiPresetID(t), RemoteModel: model}
		if encoded := base64.StdEncoding.EncodedLen(remotePieceBytes(cfg) + wavHeaderBytes); encoded > limit {
			t.Errorf("%s: a full piece encodes to %d bytes, over the %d limit", model, encoded, limit)
		}
	}
}
