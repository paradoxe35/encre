package ai

import (
	"bufio"
	"bytes"
	"io"
)

const maxEventSize = 1 << 20

// readEvents hands the data of each server-sent event to handle, until handle reports the stream
// done or the body ends. Comments and fields other than data are skipped.
func readEvents(body io.Reader, handle func(data []byte) (done bool, err error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventSize)

	var data []byte
	dispatch := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		done, err := handle(data)
		data = data[:0]
		return done, err
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			if done, err := dispatch(); done || err != nil {
				return err
			}
			continue
		}
		if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(value, []byte(" "))...)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err := dispatch()
	return err
}
