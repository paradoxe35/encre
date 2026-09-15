package platform

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildICO assembles an icon file whose frames are just filler bytes, enough
// for the directory logic under test.
func buildICO(sizes ...int) []byte {
	header := make([]byte, icoHeaderSize+len(sizes)*icoEntrySize)
	binary.LittleEndian.PutUint16(header[2:4], 1)
	binary.LittleEndian.PutUint16(header[4:6], uint16(len(sizes)))

	var body []byte
	for i, size := range sizes {
		entry := header[icoHeaderSize+i*icoEntrySize:]
		entry[0], entry[1] = byte(size%256), byte(size%256)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(size))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(len(header)+len(body)))
		body = append(body, make([]byte, size)...)
	}
	return append(header, body...)
}

func TestIcoFramesReadsDirectory(t *testing.T) {
	frames, err := icoFrames(buildICO(16, 32, 256))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	if frames[2].width != 256 || frames[2].height != 256 {
		t.Errorf("a zero size byte should read as 256, got %dx%d", frames[2].width, frames[2].height)
	}
	if len(frames[1].data) != 32 {
		t.Errorf("frame data is %d bytes, want 32", len(frames[1].data))
	}
}

func TestIcoFramesRejectsBadInput(t *testing.T) {
	cases := map[string][]byte{
		"empty":     nil,
		"png":       []byte("\x89PNG\r\n\x1a\n"),
		"truncated": buildICO(16, 32)[:icoHeaderSize+icoEntrySize],
		"overflow":  buildICO(16, 32)[:icoHeaderSize+2*icoEntrySize+10],
	}
	for name, ico := range cases {
		if _, err := icoFrames(ico); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPickFramePrefersExactThenLarger(t *testing.T) {
	frames, err := icoFrames(buildICO(16, 24, 48, 256))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct{ want, expect int }{
		{16, 16},
		{24, 24},
		{20, 24},   // no 20: shrink the next size up
		{32, 48},   // no 32: 48 beats 24, which would stretch
		{300, 256}, // nothing large enough: the largest there is
	}
	for _, c := range cases {
		frame, ok := pickFrame(frames, c.want)
		if !ok || frame.width != c.expect {
			t.Errorf("pickFrame(%d) = %d, want %d", c.want, frame.width, c.expect)
		}
	}

	if _, ok := pickFrame(nil, 16); ok {
		t.Error("pickFrame with no frames should report none")
	}
}

// The shipped icon must carry the sizes Windows asks for at common scale
// factors, so the small icon is never a shrunk 256 px render.
func TestShippedIconHasWindowsSizes(t *testing.T) {
	ico, err := os.ReadFile(filepath.Join("..", "..", "assets", "icon.ico"))
	if err != nil {
		t.Skip("assets/icon.ico not available:", err)
	}

	frames, err := icoFrames(ico)
	if err != nil {
		t.Fatal(err)
	}

	have := map[int]bool{}
	for _, frame := range frames {
		have[frame.width] = true
	}
	// 100%, 125%, 150% and 200% scaling, small and big.
	for _, size := range []int{16, 20, 24, 32, 40, 48, 64} {
		if !have[size] {
			t.Errorf("assets/icon.ico has no %d px frame; run scripts/generate_icons.py", size)
		}
	}
}
