package revision

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/config"
)

// stopResult is what one StopRecording call returns; a gate holds the call open until closed.
type stopResult struct {
	text string
	err  error
	gate chan struct{}
}

type fakeSpeech struct {
	mu       sync.Mutex
	calls    []string
	startErr error
	// stops are handed out in call order; the last one repeats.
	stops []stopResult
}

func (f *fakeSpeech) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeSpeech) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeSpeech) Prepare(config.SpeechConfig) error { f.record("prepare"); return nil }
func (f *fakeSpeech) StartRecording(config.SpeechConfig) error {
	f.record("start")
	return f.startErr
}
func (f *fakeSpeech) StopRecording() (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "stop")
	next := f.stops[0]
	if len(f.stops) > 1 {
		f.stops = f.stops[1:]
	}
	f.mu.Unlock()

	if next.gate != nil {
		<-next.gate
	}
	return next.text, next.err
}
func (f *fakeSpeech) Close() { f.record("close") }

type fakeTypist struct {
	mu       sync.Mutex
	cleaned  string
	cleanErr error
	typed    []string
	insErr   error
	history  [][2]string
}

func (f *fakeTypist) CleanTranscript(string) (string, error) {
	if f.cleanErr != nil {
		return "", f.cleanErr
	}
	return f.cleaned, nil
}

func (f *fakeTypist) InsertText(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, text)
	return f.insErr
}

func (f *fakeTypist) RecordSpeech(raw, final string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history = append(f.history, [2]string{raw, final})
}

func (f *fakeTypist) typedSoFar() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.typed)
}

func (f *fakeTypist) remembered() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.history)
}

type harness struct {
	dictation *Dictation
	speech    *fakeSpeech
	typist    *fakeTypist
	reported  chan error
}

func newHarness(t *testing.T, cleanUp bool) *harness {
	t.Helper()
	cfg := config.Default()
	speech := cfg.SpeechSettings()
	speech.CleanUp = cleanUp
	cfg.SetSpeechSettings(speech)

	h := &harness{
		speech:   &fakeSpeech{stops: []stopResult{{text: "hello world"}}},
		typist:   &fakeTypist{cleaned: "Hello, world."},
		reported: make(chan error, 8),
	}
	h.dictation = newDictation(h.speech, h.typist, func() *config.Config { return cfg }, func(err error) {
		h.reported <- err
	})
	return h
}

// A take is a press followed by a release.
func (h *harness) take() {
	h.dictation.Toggle(true)
	h.dictation.Toggle(false)
}

func (h *harness) expectReport(t *testing.T, want error) {
	t.Helper()
	select {
	case got := <-h.reported:
		if !errors.Is(got, want) {
			t.Fatalf("reported %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("nothing reported, want %v", want)
	}
}

func (h *harness) expectNoReport(t *testing.T) {
	t.Helper()
	select {
	case got := <-h.reported:
		t.Fatalf("unexpected report: %v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func (h *harness) waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (h *harness) waitTyped(t *testing.T, n int) []string {
	t.Helper()
	h.waitUntil(t, "typed takes", func() bool { return len(h.typist.typedSoFar()) >= n })
	return h.typist.typedSoFar()
}

func (h *harness) waitStops(t *testing.T, n int) {
	t.Helper()
	h.waitUntil(t, "stop calls", func() bool {
		return slices.Index(h.speech.recorded(), "stop") >= 0 && countOf(h.speech.recorded(), "stop") >= n
	})
}

func countOf(calls []string, call string) int {
	n := 0
	for _, c := range calls {
		if c == call {
			n++
		}
	}
	return n
}

func TestATakeIsTypedAndRemembered(t *testing.T) {
	h := newHarness(t, false)
	h.take()

	if typed := h.waitTyped(t, 1); typed[0] != "hello world" {
		t.Fatalf("typed %q", typed[0])
	}
	h.expectNoReport(t)
	if got := h.typist.remembered(); len(got) != 1 || got[0] != [2]string{"hello world", "hello world"} {
		t.Fatalf("history %v", got)
	}
}

func TestCleanUpRunsWhenEnabledAndKeepsTheRawTake(t *testing.T) {
	h := newHarness(t, true)
	h.take()

	if typed := h.waitTyped(t, 1); typed[0] != "Hello, world." {
		t.Fatalf("typed %q, want the cleaned text", typed[0])
	}
	h.expectNoReport(t)
	if got := h.typist.remembered(); got[0] != [2]string{"hello world", "Hello, world."} {
		t.Fatalf("history %v, want raw and cleaned", got)
	}
}

func TestACleanUpFailureIsReportedAndNothingIsTyped(t *testing.T) {
	h := newHarness(t, true)
	boom := errors.New("provider down")
	h.typist.cleanErr = boom
	h.take()

	h.expectReport(t, boom)
	if typed := h.typist.typedSoFar(); len(typed) != 0 {
		t.Fatalf("typed %v after a failed clean-up", typed)
	}
}

func TestAnEmptyTranscriptIsReportedNotSwallowed(t *testing.T) {
	for _, text := range []string{"", "   ", "\n\t"} {
		h := newHarness(t, false)
		h.speech.stops = []stopResult{{text: text}}
		h.take()

		h.expectReport(t, ErrNoSpeech)
		if typed := h.typist.typedSoFar(); len(typed) != 0 {
			t.Fatalf("typed %v for transcript %q", typed, text)
		}
	}
}

func TestAStopErrorIsReported(t *testing.T) {
	h := newHarness(t, false)
	boom := errors.New("model missing")
	h.speech.stops = []stopResult{{err: boom}}
	h.take()

	h.expectReport(t, boom)
	if typed := h.typist.typedSoFar(); len(typed) != 0 {
		t.Fatalf("typed %v after a failed stop", typed)
	}
}

func TestAStartErrorIsReportedAndTheReleaseIsIgnored(t *testing.T) {
	h := newHarness(t, false)
	boom := errors.New("microphone busy")
	h.speech.startErr = boom
	h.take()

	h.expectReport(t, boom)
	if calls := h.speech.recorded(); slices.Contains(calls, "stop") {
		t.Fatalf("recorder saw %v: stop was sent for a take that never started", calls)
	}
}

func TestAnInsertFailureIsReportedAndNotRemembered(t *testing.T) {
	h := newHarness(t, false)
	boom := errors.New("paste refused")
	h.typist.insErr = boom
	h.take()

	h.expectReport(t, boom)
	if got := h.typist.remembered(); len(got) != 0 {
		t.Fatalf("history %v after a failed paste", got)
	}
}

func TestASecondPressWhileRunningIsIgnored(t *testing.T) {
	h := newHarness(t, false)
	h.dictation.Toggle(true)
	h.dictation.Toggle(true)
	h.dictation.Toggle(false)
	h.dictation.Toggle(false)

	h.waitTyped(t, 1)
	if got := h.speech.recorded(); !slices.Equal(got, []string{"start", "stop"}) {
		t.Fatalf("recorder saw %v, want one start and one stop", got)
	}
}

func TestAReleaseWithoutAPressDoesNothing(t *testing.T) {
	h := newHarness(t, false)
	h.dictation.Toggle(false)

	h.expectNoReport(t)
	if got := h.speech.recorded(); len(got) != 0 {
		t.Fatalf("recorder saw %v", got)
	}
}

func TestTakesAreTypedInTheOrderSpoken(t *testing.T) {
	h := newHarness(t, false)
	first := make(chan struct{})
	h.speech.stops = []stopResult{{text: "first", gate: first}, {text: "second"}}

	h.take()
	h.waitStops(t, 1)

	// The second take finishes transcribing before the first, and must still wait.
	h.take()
	h.waitStops(t, 2)
	time.Sleep(30 * time.Millisecond)
	if typed := h.typist.typedSoFar(); len(typed) != 0 {
		t.Fatalf("typed %v before the first take finished", typed)
	}

	close(first)
	if typed := h.waitTyped(t, 2); !slices.Equal(typed, []string{"first", "second"}) {
		t.Fatalf("typed %v, want first then second", typed)
	}
}

func TestAFailedTakeDoesNotHoldUpTheNextOne(t *testing.T) {
	h := newHarness(t, false)
	first := make(chan struct{})
	boom := errors.New("model missing")
	h.speech.stops = []stopResult{{err: boom, gate: first}, {text: "second"}}

	h.take()
	h.waitStops(t, 1)
	h.take()
	h.waitStops(t, 2)
	close(first)

	h.expectReport(t, boom)
	if typed := h.waitTyped(t, 1); typed[0] != "second" {
		t.Fatalf("typed %v", typed)
	}
}

func TestCloseReachesTheRecorder(t *testing.T) {
	h := newHarness(t, false)
	h.dictation.Close()
	if got := h.speech.recorded(); !slices.Equal(got, []string{"close"}) {
		t.Fatalf("recorder saw %v", got)
	}
}
