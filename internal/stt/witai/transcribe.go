package witai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const endpoint = "https://api.wit.ai/speech"

const (
	captureSampleRate = 16_000 // what encre_stt_stop_pcm delivers
	witSampleRate     = 8_000  // required by the Wit.ai speech endpoint
	bytesPerSample    = 2
	chunkSeconds      = 20
	bytesPerChunk     = witSampleRate * bytesPerSample * chunkSeconds
)

var client = &http.Client{Timeout: 30 * time.Second}

// Transcribe sends pcm - headerless 16-bit signed little-endian PCM, mono, at
// captureSampleRate - to Wit.ai for lang and returns the recognized text. Wit.ai
// caps how much audio one request may carry, so pcm is downsampled to 8 kHz and
// split into chunkSeconds windows, transcribed in order, and joined.
func Transcribe(ctx context.Context, pcm []byte, lang string) (string, error) {
	token, ok := tokenFor(lang)
	if !ok {
		return "", fmt.Errorf("no Wit.ai key for language %q", lang)
	}

	downsampled := downsample(pcm)
	if len(downsampled) == 0 {
		return "", nil
	}

	var parts []string
	for start := 0; start < len(downsampled); start += bytesPerChunk {
		end := min(start+bytesPerChunk, len(downsampled))

		text, err := transcribeChunk(ctx, token, downsampled[start:end])
		if err != nil {
			return "", err
		}
		if text != "" {
			parts = append(parts, text)
		}
	}

	return strings.TrimSpace(strings.Join(parts, " ")), nil
}

func transcribeChunk(ctx context.Context, token string, chunk []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(chunk))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.wit.20180705+json")
	req.Header.Set("Content-Type", "audio/raw;encoding=signed-integer;bits=16;rate=8000;endian=little")

	q := req.URL.Query()
	q.Set("verbose", "true")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("wit.ai returned %s: %s", resp.Status, bytes.TrimSpace(body))
	}

	return parseResponse(body)
}

type response struct {
	Text   string `json:"text"`
	Legacy string `json:"_text"`
	Error  string `json:"error"`
}

// parseResponse reads a single Wit.ai JSON object, falling back to the legacy
// _text field; an error field is a failure.
func parseResponse(body []byte) (string, error) {
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("invalid wit.ai response: %w", err)
	}
	if r.Error != "" {
		return "", fmt.Errorf("wit.ai: %s", r.Error)
	}
	if r.Text != "" {
		return r.Text, nil
	}
	return r.Legacy, nil
}

// downsample halves the sample rate by averaging consecutive sample pairs,
// converting 16-bit LE PCM at captureSampleRate to 16-bit LE PCM at witSampleRate.
func downsample(pcm []byte) []byte {
	samples := len(pcm) / bytesPerSample
	out := make([]byte, 0, len(pcm)/2)

	for i := 0; i+1 < samples; i += 2 {
		a := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		b := int16(binary.LittleEndian.Uint16(pcm[(i+1)*2:]))
		avg := int16((int32(a) + int32(b)) / 2)

		var buf [2]byte
		binary.LittleEndian.PutUint16(buf[:], uint16(avg))
		out = append(out, buf[:]...)
	}

	return out
}
