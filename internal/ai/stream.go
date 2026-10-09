package ai

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
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

// turn gathers one streamed reply: its text as it arrives, and the tool calls it builds up.
type turn struct {
	onText func(string)
	text   strings.Builder
	calls  []ToolCall
	// pending maps the protocol's index for a call still streaming to its place in calls.
	pending map[int]int
}

func (t *turn) write(text string) {
	if text == "" {
		return
	}
	t.text.WriteString(text)
	if t.onText != nil {
		t.onText(text)
	}
}

// call is the call streaming under the protocol's index, begun on first sight.
func (t *turn) call(index int) *ToolCall {
	if t.pending == nil {
		t.pending = make(map[int]int)
	}
	at, ok := t.pending[index]
	if !ok {
		at = len(t.calls)
		t.pending[index] = at
		t.calls = append(t.calls, ToolCall{})
	}
	return &t.calls[at]
}

// add records a call that arrived whole.
func (t *turn) add(call ToolCall) {
	t.calls = append(t.calls, call)
}

func (t *turn) reply() Reply {
	for i := range t.calls {
		if t.calls[i].ID == "" {
			t.calls[i].ID = fmt.Sprintf("call_%d", i)
		}
	}
	return Reply{Text: t.text.String(), Calls: t.calls}
}
