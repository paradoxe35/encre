package pcm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// maxStreak is how many pieces in a row may fail before the rest of a take is given up on.
const maxStreak = 3

// Failures collects the pieces of a take that failed, and says when the rest are not worth sending:
// a timeout or a refused request would fail them all, at the cost of a full upload each.
type Failures struct {
	total, streak int
	failed        []int
	first         error
	skipped       bool
}

func NewFailures(total int) *Failures { return &Failures{total: total} }

// Failed records piece i, counted from zero, and reports whether the pieces after it are still worth sending.
func (f *Failures) Failed(i int, err error) bool {
	f.failed = append(f.failed, i+1)
	if f.first == nil {
		f.first = err
	}
	f.streak++
	goOn := transient(err) && f.streak < maxStreak
	if !goOn && i+1 < f.total {
		f.skipped = true
	}
	return goOn
}

func (f *Failures) Succeeded() { f.streak = 0 }

// Err names the cause first, so a notification cut short still shows it.
func (f *Failures) Err() error {
	if f.first == nil || f.total <= 1 {
		return f.first
	}
	rest := ""
	if f.skipped {
		rest = ", the rest not sent"
	}
	return fmt.Errorf("%w (part %s of %d failed%s)", f.first, spans(f.failed), f.total, rest)
}

// transient is anything but what every piece would meet in turn: a deadline or a timed-out line,
// or a service's refusal of the request itself (a 4xx other than 408 and 429). One piece cut off at
// the model's output limit, refused, or answered with garbage says nothing about the next.
func transient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return false
	}
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) {
		code := status.HTTPStatus()
		return code < 400 || code >= 500 || code == 408 || code == 429
	}
	return true
}

// spans writes ascending numbers as ranges: 1–3, 7.
func spans(numbers []int) string {
	var out []string
	for i := 0; i < len(numbers); {
		j := i
		for j+1 < len(numbers) && numbers[j+1] == numbers[j]+1 {
			j++
		}
		if j > i {
			out = append(out, strconv.Itoa(numbers[i])+"–"+strconv.Itoa(numbers[j]))
		} else {
			out = append(out, strconv.Itoa(numbers[i]))
		}
		i = j + 1
	}
	return strings.Join(out, ", ")
}
