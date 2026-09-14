package stt

import (
	"context"
	"encoding/binary"
	"strings"
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
		{"gpt-4o-transcribe", "languages[]"},
		{"gpt-4o-mini-transcribe", "languages[]"},
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

func TestRemoteTranscribeSizeGuard(t *testing.T) {
	pcm := make([]byte, remoteMaxAudioBytes+1)
	cfg := config.SpeechConfig{RemoteBaseURL: "https://example.invalid/v1", RemoteModel: "whisper-1"}

	_, err := RemoteTranscribe(context.Background(), cfg, pcm)
	if err == nil {
		t.Fatal("expected a size-limit error")
	}
	if !strings.Contains(err.Error(), "25 MB") {
		t.Fatalf("expected the error to name the limit, got %q", err)
	}
}

func TestRemoteAPIKeyFallsBackToPlaintext(t *testing.T) {
	cfg := config.SpeechConfig{RemoteAPIKey: "sk-plainkey-not-encrypted"}
	if got := remoteAPIKey(cfg); got != "sk-plainkey-not-encrypted" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoteAPIKeyDecrypts(t *testing.T) {
	encrypted, err := config.EncryptAPIKey("sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.SpeechConfig{RemoteAPIKey: encrypted}
	if got := remoteAPIKey(cfg); got != "sk-secret" {
		t.Fatalf("got %q", got)
	}
}
