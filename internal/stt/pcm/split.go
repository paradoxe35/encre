// Package pcm splits 16-bit mono audio for services that cap how much one request may carry.
package pcm

import (
	"encoding/binary"
	"math"
)

const bytesPerSample = 2

// Split cuts pcm into pieces of at most limit bytes, each at the quietest moment of the second
// before the limit, so a word is rarely split between two requests.
func Split(pcm []byte, limit, sampleRate int) [][]byte {
	limit -= limit % bytesPerSample
	if limit <= 0 || len(pcm) <= limit {
		return [][]byte{pcm}
	}
	var pieces [][]byte
	for len(pcm) > limit {
		cut := quietest(pcm, limit, sampleRate)
		pieces = append(pieces, pcm[:cut])
		pcm = pcm[cut:]
	}
	if len(pcm) > 0 {
		pieces = append(pieces, pcm)
	}
	return pieces
}

// quietest is the middle of the 20 ms frame with the least energy in the second before limit,
// or limit itself when no frame fits.
func quietest(pcm []byte, limit, sampleRate int) int {
	frame := max(sampleRate/50, 1) * bytesPerSample
	start := max(limit-sampleRate*bytesPerSample, limit/2)
	cut, least := limit, int64(math.MaxInt64)
	for end := limit; end-frame >= start; end -= frame {
		var energy int64
		for i := end - frame; i < end; i += bytesPerSample {
			sample := int64(int16(binary.LittleEndian.Uint16(pcm[i:])))
			energy += sample * sample
		}
		if energy < least {
			cut, least = end-frame/2, energy
		}
	}
	return cut - cut%bytesPerSample
}
