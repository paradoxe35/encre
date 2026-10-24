package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/stt/pcm"
)

const (
	remoteSampleRate     = 16_000 // what encre_stt_stop_pcm delivers
	remoteBitsPerSample  = 16
	remoteChannels       = 1
	wavHeaderBytes       = 44
	remoteMaxAudioBytes  = 25 * 1024 * 1024 // OpenAI and Groq's free tier both cap uploads here
	remoteRequestTimeout = 3 * time.Minute

	remoteMaxResponseBytes = 4 << 20
	remoteMaxErrorBytes    = 4 << 10
	remoteMaxErrorChars    = 300
)

var errUndecryptableKey = errors.New("the saved API key could not be decrypted; re-enter it in Settings")

var remoteClient = &http.Client{Timeout: remoteRequestTimeout}

// audio is headerless 16-bit signed little-endian mono at remoteSampleRate. A take over what one
// request may carry is sent in pieces cut at pauses; one failing does not cost the others, whose
// text comes back with the error.
func RemoteTranscribe(ctx context.Context, cfg config.SpeechConfig, audio []byte) (string, error) {
	// Nothing was heard: a header-only file costs a request, and some services reject it as too short.
	if len(audio) == 0 {
		return "", nil
	}

	pieces := pcm.Split(audio, remotePieceBytes(cfg), remoteSampleRate)
	var parts, failed []string
	var firstErr error
	for i, piece := range pieces {
		pieceCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
		text, err := transcribeWAV(pieceCtx, cfg, wavFile(piece))
		cancel()
		if err != nil {
			failed = append(failed, strconv.Itoa(i+1))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		parts = append(parts, text)
	}
	if firstErr != nil && len(pieces) > 1 {
		firstErr = fmt.Errorf("part %s of %d: %w", strings.Join(failed, ", "), len(pieces), firstErr)
	}
	return pcm.Join(parts), firstErr
}

// remoteUploadSafeBytes keeps a piece under both OpenAI's 25 MiB, which counts the whole form, and
// a 25 MB read as millions, with room for the form's other fields.
const remoteUploadSafeBytes = 25_000_000 - 64<<10

// remotePieceBytes is the most audio one request carries, less its WAV header; Gemini sends it
// as base64, a third larger.
func remotePieceBytes(cfg config.SpeechConfig) int {
	if protocolFor(cfg.RemoteProvider) == ProtocolGemini {
		limit := geminiMaxRequestBytes
		if IsGeminiTranscribeModel(cfg.RemoteModel) {
			limit = interactionsMaxRequestBytes
		}
		return limit/4*3 - wavHeaderBytes
	}
	return remoteUploadSafeBytes - wavHeaderBytes
}

func transcribeWAV(ctx context.Context, cfg config.SpeechConfig, wav []byte) (string, error) {
	if protocolFor(cfg.RemoteProvider) == ProtocolGemini {
		return geminiTranscribe(ctx, cfg, wav)
	}

	if len(wav) > remoteMaxAudioBytes {
		return "", fmt.Errorf("recording is too large for the hosted service (limit is %d MB)", remoteMaxAudioBytes/(1024*1024))
	}

	key, err := remoteAPIKey(cfg)
	if err != nil {
		return "", err
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

	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := remoteClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := readRemoteBody(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("hosted service returned %s: %s", resp.Status, remoteErrorMessage(respBody, resp.Status))
	}

	return parseRemoteResponse(respBody)
}

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

// Only gpt-transcribe takes a repeated languages[]; gpt-4o-transcribe and the rest keep the one language.
func remoteLanguageField(model string) string {
	if isGPTTranscribe(model) {
		return "languages[]"
	}
	return "language"
}

func isGPTTranscribe(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "gpt-transcribe" || strings.HasPrefix(model, "gpt-transcribe-")
}

// Falls back to the raw value for a key stored in plaintext.
func remoteAPIKey(cfg config.SpeechConfig) (string, error) {
	if cfg.RemoteAPIKey == "" {
		return "", nil
	}
	plain, err := config.DecryptAPIKey(cfg.RemoteAPIKey)
	if err == nil {
		return plain, nil
	}
	if config.LooksEncrypted(cfg.RemoteAPIKey) {
		return "", errUndecryptableKey
	}
	return cfg.RemoteAPIKey, nil
}

// Bounded, so a misbehaving server can neither fill memory nor paste a whole HTML page into an error.
func readRemoteBody(resp *http.Response) ([]byte, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return io.ReadAll(io.LimitReader(resp.Body, remoteMaxErrorBytes))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, remoteMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > remoteMaxResponseBytes {
		return nil, fmt.Errorf("the response is larger than %d MB", remoteMaxResponseBytes>>20)
	}
	return body, nil
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

// Matches both the OpenAI {"error":{"message":...}} shape and a flat {"error":"..."}.
func remoteErrorMessage(body []byte, status string) string {
	var withObject struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &withObject); err == nil && withObject.Error.Message != "" {
		return truncateText(withObject.Error.Message, remoteMaxErrorChars)
	}

	var withString struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &withString); err == nil && withString.Error != "" {
		return truncateText(withString.Error, remoteMaxErrorChars)
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed != "" {
		return truncateText(trimmed, remoteMaxErrorChars)
	}
	return status
}

func truncateText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// A canonical 44-byte header, so the audio posts as audio/wav without a WAV dependency.
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

	buf := make([]byte, wavHeaderBytes)
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
