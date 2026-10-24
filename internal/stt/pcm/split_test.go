package pcm

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func tone(samples int, level int16) []byte {
	out := make([]byte, samples*bytesPerSample)
	for i := range samples {
		value := level
		if i%2 == 1 {
			value = -level
		}
		binary.LittleEndian.PutUint16(out[i*bytesPerSample:], uint16(value))
	}
	return out
}

func TestSplitKeepsAShortTakeWhole(t *testing.T) {
	pcm := tone(100, 1000)
	if got := Split(pcm, len(pcm), 16_000); len(got) != 1 || !bytes.Equal(got[0], pcm) {
		t.Fatalf("got %d pieces", len(got))
	}
}

func TestSplitCutsAtThePauseBeforeTheLimit(t *testing.T) {
	const rate = 1_000
	speech, pause := tone(rate*3/2, 8000), tone(rate/10, 0)
	pcm := append(append(append([]byte{}, speech...), pause...), tone(rate*2, 8000)...)
	limit := 2 * rate * bytesPerSample

	pieces := Split(pcm, limit, rate)
	if len(pieces) < 2 {
		t.Fatalf("got %d pieces, want the take split", len(pieces))
	}
	if !bytes.Equal(bytes.Join(pieces, nil), pcm) {
		t.Fatal("the pieces do not add up to the take")
	}
	cut := len(pieces[0])
	if cut < len(speech) || cut > len(speech)+len(pause) {
		t.Errorf("cut at byte %d, want inside the pause at %d-%d", cut, len(speech), len(speech)+len(pause))
	}
	for i, piece := range pieces {
		if len(piece) > limit || len(piece)%bytesPerSample != 0 {
			t.Errorf("piece %d is %d bytes, want at most %d and whole samples", i, len(piece), limit)
		}
	}
}

func TestSplitWithoutAPauseStillFitsTheLimit(t *testing.T) {
	pcm := tone(10_001, 5000)
	for _, piece := range Split(pcm, 3_001, 16_000) {
		if len(piece) > 3_000 || len(piece) == 0 {
			t.Fatalf("piece of %d bytes", len(piece))
		}
	}
}
