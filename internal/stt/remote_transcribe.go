package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/paradoxe35/encre/internal/config"
)

const (
	remoteSampleRate     = 16_000 // what encre_stt_stop_pcm delivers
	remoteBitsPerSample  = 16
	remoteChannels       = 1
	remoteMaxAudioBytes  = 25 * 1024 * 1024 // OpenAI and Groq's free tier both cap uploads here
	remoteRequestTimeout = 3 * time.Minute
)

var remoteClient = &http.Client{Timeout: remoteRequestTimeout}

// RemoteTranscribe posts pcm - headerless 16-bit signed little-endian mono PCM at
// remoteSampleRate, as returned by FFISpeech.StopPCM - to the hosted service
// configured in cfg and returns the recognized text.
func RemoteTranscribe(ctx context.Context, cfg config.SpeechConfig, pcm []byte) (string, error) {
	wav := wavFile(pcm)
	if protocolFor(cfg.RemoteProvider) == ProtocolGemini {
		return geminiTranscribe(ctx, cfg, wav)
	}

	if len(wav) > remoteMaxAudioBytes {
		return "", fmt.Errorf("recording is too large for the hosted service (limit is %d MB)", remoteMaxAudioBytes/(1024*1024))
	}

	body, contentType, err := remoteRequestBody(cfg, wav)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(cfg.RemoteBaseURL, "/") + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)

	if key := remoteAPIKey(cfg); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := remoteClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("hosted service returned %s: %s", resp.Status, remoteErrorMessage(respBody, resp.Status))
	}

	return parseRemoteResponse(respBody)
}

// remoteRequestBody builds the multipart form: the audio file, the model, and -
// when a language is configured - the field name whose spelling depends on the model.
func remoteRequestBody(cfg config.SpeechConfig, wav []byte) (io.Reader, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(wav); err != nil {
		return nil, "", err
	}

	if err := writer.WriteField("model", cfg.RemoteModel); err != nil {
		return nil, "", err
	}

	if cfg.Language != "" {
		if err := writer.WriteField(remoteLanguageField(cfg.RemoteModel), cfg.Language); err != nil {
			return nil, "", err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	return &buf, writer.FormDataContentType(), nil
}

// remoteLanguageField picks the form field OpenAI expects for language: the
// gpt- transcribe models take a repeated languages[], everything else (whisper-1,
// every Groq model, and unrecognized custom-endpoint models) takes language.
func remoteLanguageField(model string) string {
	if strings.HasPrefix(strings.ToLower(model), "gpt-") {
		return "languages[]"
	}
	return "language"
}

// remoteAPIKey decrypts cfg.RemoteAPIKey, falling back to the raw value when it
// predates encryption - a config written before that change holds it in plaintext.
func remoteAPIKey(cfg config.SpeechConfig) string {
	if cfg.RemoteAPIKey == "" {
		return ""
	}
	plain, err := config.DecryptAPIKey(cfg.RemoteAPIKey)
	if err != nil {
		return cfg.RemoteAPIKey
	}
	return plain
}

type remoteResponse struct {
	Text string `json:"text"`
}

func parseRemoteResponse(body []byte) (string, error) {
	var r remoteResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("invalid hosted service response: %w", err)
	}
	return r.Text, nil
}

// remoteErrorMessage extracts a provider error message from a non-2xx body,
// matching both the OpenAI {"error":{"message":...}} shape and a flat {"error":"..."}.
func remoteErrorMessage(body []byte, status string) string {
	var withObject struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &withObject); err == nil && withObject.Error.Message != "" {
		return withObject.Error.Message
	}

	var withString struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &withString); err == nil && withString.Error != "" {
		return withString.Error
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed != "" {
		return trimmed
	}
	return status
}

// wavFile prefixes headerless 16-bit LE mono PCM with a canonical 44-byte WAV
// header, so it can be posted as audio/wav without pulling in a WAV dependency.
func wavFile(pcm []byte) []byte {
	header := wavHeader(len(pcm))
	out := make([]byte, 0, len(header)+len(pcm))
	out = append(out, header...)
	out = append(out, pcm...)
	return out
}

func wavHeader(dataLen int) []byte {
	byteRate := remoteSampleRate * remoteChannels * remoteBitsPerSample / 8
	blockAlign := remoteChannels * remoteBitsPerSample / 8

	buf := make([]byte, 44)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")

	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // AudioFormat: PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(remoteChannels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(remoteSampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], uint16(remoteBitsPerSample))

	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))

	return buf
}
