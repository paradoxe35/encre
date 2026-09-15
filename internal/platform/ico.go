package platform

import (
	"encoding/binary"
	"fmt"
)

type icoFrame struct {
	width  int
	height int
	data   []byte
}

const (
	icoHeaderSize = 6
	icoEntrySize  = 16
)

func icoFrames(ico []byte) ([]icoFrame, error) {
	if len(ico) < icoHeaderSize {
		return nil, fmt.Errorf("ico: %d bytes is too short for a header", len(ico))
	}
	if binary.LittleEndian.Uint16(ico[0:2]) != 0 || binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		return nil, fmt.Errorf("ico: not an icon file")
	}

	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	if count == 0 {
		return nil, fmt.Errorf("ico: no images")
	}
	if len(ico) < icoHeaderSize+count*icoEntrySize {
		return nil, fmt.Errorf("ico: directory of %d entries is truncated", count)
	}

	frames := make([]icoFrame, 0, count)
	for i := range count {
		entry := ico[icoHeaderSize+i*icoEntrySize:]

		width, height := int(entry[0]), int(entry[1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}

		size := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if size <= 0 || offset < 0 || offset > len(ico)-size {
			return nil, fmt.Errorf("ico: image %d (%dx%d) lies outside the file", i, width, height)
		}

		frames = append(frames, icoFrame{width: width, height: height, data: ico[offset : offset+size]})
	}
	return frames, nil
}

// Exact size, else the smallest larger one, else the largest there is.
func pickFrame(frames []icoFrame, size int) (icoFrame, bool) {
	var best icoFrame
	found := false

	for _, frame := range frames {
		if frame.width != frame.height {
			continue
		}
		if frame.width == size {
			return frame, true
		}

		switch {
		case !found:
			best, found = frame, true
		case best.width < size && frame.width > best.width:
			best = frame
		case best.width > size && frame.width >= size && frame.width < best.width:
			best = frame
		}
	}

	return best, found
}
