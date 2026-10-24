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

	"github.com/paradoxe35/encre/internal/stt/pcm"
)

var endpoint = "https://api.wit.ai/speech"

// A chunk has chunkTimeout; the whole take gets chunkBudget per chunk on top of one chunkTimeout.
var chunkTimeout, chunkBudget = 30 * time.Second, 15 * time.Second

const (
	captureSampleRate = 16_000 // what encre_stt_stop_pcm delivers
	witSampleRate     = 8_000  // required by the Wit.ai speech endpoint
	bytesPerSample    = 2
	chunkSeconds      = 20
	bytesPerChunk     = witSampleRate * bytesPerSample * chunkSeconds

	maxResponseBytes = 1 << 20
	maxErrorBytes    = 4 << 10
	maxErrorChars    = 300
)

var client = &http.Client{}

// Wit.ai caps audio per request, so it is downsampled to 8 kHz, cut at pauses, and joined.
// A failed chunk still returns the text of the chunks before it, along with the error.
func Transcribe(ctx context.Context, audio []byte, lang string) (string, error) {
	token, ok := tokenFor(lang)
	if !ok {
		return "", fmt.Errorf("no Wit.ai key for language %q", lang)
	}

	downsampled := downsample(audio)
	if len(downsampled) == 0 {
		return "", nil
	}

	pieces := pcm.Split(downsampled, bytesPerChunk, witSampleRate)
	chunks := len(pieces)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(chunks)*chunkBudget+chunkTimeout)
	defer cancel()

	var parts []string
	joined := func() string { return pcm.Join(parts) }
	for i, piece := range pieces {
		text, err := transcribeChunk(ctx, token, piece)
		if err != nil {
			return joined(), fmt.Errorf("wit.ai chunk %d of %d: %w", i+1, chunks, err)
		}
		if text != "" {
			parts = append(parts, text)
		}
	}

	return joined(), nil
}

func transcribeChunk(ctx context.Context, token string, chunk []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, chunkTimeout)
	defer cancel()

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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return "", fmt.Errorf("wit.ai returned %s: %s", resp.Status, truncate(strings.TrimSpace(string(body)), maxErrorChars))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxResponseBytes {
		return "", fmt.Errorf("wit.ai response is larger than %d MB", maxResponseBytes>>20)
	}

	return parseResponse(body)
}

func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

type response struct {
	Text   string `json:"text"`
	Legacy string `json:"_text"`
	Error  string `json:"error"`
}

// Falls back to Wit's older _text field; an error field is a failure.
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
