package pcm

import (
	"errors"
	"strings"
	"testing"
)

type statusError int

func (e statusError) Error() string   { return "returned " + strings.Repeat("x", 3) }
func (e statusError) HTTPStatus() int { return int(e) }

func TestATransientFailureLetsTheRestBeSent(t *testing.T) {
	f := NewFailures(6)
	if !f.Failed(1, statusError(503)) {
		t.Fatal("gave up after one 503")
	}
	f.Succeeded()
	if !f.Failed(3, statusError(429)) {
		t.Fatal("gave up after a 429")
	}
	if err := f.Err(); err == nil || !strings.Contains(err.Error(), "part 2, 4 of 6 failed)") || strings.Contains(err.Error(), "not sent") {
		t.Fatalf("got %v", err)
	}
}

func TestAFailureEveryPieceWouldShareStopsTheTake(t *testing.T) {
	for name, err := range map[string]error{"refused key": statusError(401), "timeout": errors.New("context deadline exceeded")} {
		f := NewFailures(90)
		if f.Failed(0, err) {
			t.Errorf("%s: went on sending", name)
		}
		got := f.Err()
		if !errors.Is(got, err) || !strings.HasPrefix(got.Error(), err.Error()) || !strings.Contains(got.Error(), "the rest not sent") {
			t.Errorf("%s: got %v, want the cause first and the rest named as not sent", name, got)
		}
	}
}

func TestFailuresInARowStopTheTake(t *testing.T) {
	f := NewFailures(10)
	f.Failed(0, statusError(500))
	f.Failed(1, statusError(500))
	if f.Failed(2, statusError(500)) {
		t.Fatal("went on after three failures in a row")
	}
	if got := f.Err().Error(); !strings.Contains(got, "part 1–3 of 10") {
		t.Fatalf("got %q", got)
	}
}

func TestAOnePieceTakeKeepsItsError(t *testing.T) {
	err := statusError(401)
	f := NewFailures(1)
	f.Failed(0, err)
	if got := f.Err(); got != error(err) {
		t.Fatalf("got %v", got)
	}
}
