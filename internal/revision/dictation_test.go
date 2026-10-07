package revision

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/overlay"
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
	answer   string
	askErr   error
	asked    []string
	// hold, when set, keeps the answer open after its first words until it is closed or cancelled.
	hold chan struct{}
}

// Ask streams the answer a word at a time, then fails with askErr if there is one.
func (f *fakeTypist) Ask(ctx context.Context, question string, onText func(string)) (string, error) {
	f.mu.Lock()
	f.asked = append(f.asked, question)
	answer, askErr, hold := f.answer, f.askErr, f.hold
	f.mu.Unlock()

	if askErr != nil && answer == "" {
		return "", askErr
	}
	for i, word := range strings.SplitAfter(answer, " ") {
		onText(word)
		if i == 0 && hold != nil {
			select {
			case <-hold:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	if askErr != nil {
		return "", askErr
	}
	return answer, nil
}

func (f *fakeTypist) questions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
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

type fakeOverlay struct {
	mu     sync.Mutex
	calls  []string
	levels []float32
}

func (f *fakeOverlay) Show(phase overlay.Phase) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := "listening"
	switch phase {
	case overlay.Transcribing:
		name = "transcribing"
	case overlay.Thinking:
		name = "thinking"
	}
	f.calls = append(f.calls, name)
}

func (f *fakeOverlay) Level(level float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.levels = append(f.levels, level)
}

func (f *fakeOverlay) Hide() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "hide")
}

func (f *fakeOverlay) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type harness struct {
	dictation *Dictation
	speech    *fakeSpeech
	typist    *fakeTypist
	overlay   *fakeOverlay
	reported  chan error
	failed    chan config.ActionKind
	view      *fakeView
}

func newHarness(t *testing.T, cleanUp bool) *harness {
	t.Helper()
	cfg := config.Default()
	speech := cfg.SpeechSettings()
	speech.CleanUp = cleanUp
	cfg.SetSpeechSettings(speech)

	h := &harness{
		speech:   &fakeSpeech{stops: []stopResult{{text: "hello world"}}},
		typist:   &fakeTypist{cleaned: "Hello, world.", answer: "Paris."},
		overlay:  &fakeOverlay{},
		reported: make(chan error, 8),
		failed:   make(chan config.ActionKind, 8),
		view:     &fakeView{done: make(chan shownAnswer, 8)},
	}
	h.dictation = newDictation(h.speech, h.typist, func() *config.Config { return cfg },
		func(kind config.ActionKind, err error) {
			h.failed <- kind
			h.reported <- err
		},
		h.view)
	h.dictation.SetOverlay(h.overlay)
	return h
}

func (h *harness) waitOverlay(t *testing.T, last string) []string {
	t.Helper()
	h.waitUntil(t, "the indicator to "+last, func() bool {
		seen := h.overlay.seen()
		return len(seen) > 0 && seen[len(seen)-1] == last
	})
	return h.overlay.seen()
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

func TestTheIndicatorFollowsATake(t *testing.T) {
	h := newHarness(t, false)
	h.take()
	h.waitTyped(t, 1)

	if got := h.waitOverlay(t, "hide"); !slices.Equal(got, []string{"listening", "transcribing", "hide"}) {
		t.Fatalf("indicator saw %v", got)
	}
}

func TestTheIndicatorHidesAfterAFailedTake(t *testing.T) {
	h := newHarness(t, false)
	boom := errors.New("model missing")
	h.speech.stops = []stopResult{{err: boom}}
	h.take()
	h.expectReport(t, boom)

	if got := h.waitOverlay(t, "hide"); !slices.Equal(got, []string{"listening", "transcribing", "hide"}) {
		t.Fatalf("indicator saw %v", got)
	}
}

func TestAStartFailureNeverShowsTheIndicator(t *testing.T) {
	h := newHarness(t, false)
	h.speech.startErr = errors.New("microphone busy")
	h.take()
	h.expectReport(t, h.speech.startErr)

	if got := h.overlay.seen(); len(got) != 0 {
		t.Fatalf("indicator saw %v for a take that never started", got)
	}
}

func TestTheIndicatorStaysUpWhileAnotherTakeIsRunning(t *testing.T) {
	h := newHarness(t, false)
	first := make(chan struct{})
	h.speech.stops = []stopResult{{text: "first", gate: first}, {text: "second"}}

	h.take()
	h.waitStops(t, 1)
	h.dictation.Toggle(true)
	if got := h.overlay.seen(); slices.Contains(got, "hide") {
		t.Fatalf("indicator hid while a new take was recording: %v", got)
	}

	h.dictation.Toggle(false)
	h.waitStops(t, 2)
	close(first)
	h.waitTyped(t, 2)

	got := h.waitOverlay(t, "hide")
	if countOf(got, "hide") != 1 || got[len(got)-1] != "hide" {
		t.Fatalf("indicator saw %v, want exactly one hide at the end", got)
	}
}

func TestLevelsReachTheCurrentIndicatorAndTheOldOneIsHidden(t *testing.T) {
	h := newHarness(t, false)
	h.dictation.Level(0.5)

	replacement := &fakeOverlay{}
	h.dictation.SetOverlay(replacement)
	h.dictation.Level(0.7)

	if got := h.overlay.levels; !slices.Equal(got, []float32{0.5}) {
		t.Fatalf("old indicator levels %v", got)
	}
	if got := h.overlay.seen(); !slices.Equal(got, []string{"hide"}) {
		t.Fatalf("old indicator saw %v, want to be hidden when replaced", got)
	}
	if got := replacement.levels; !slices.Equal(got, []float32{0.7}) {
		t.Fatalf("replacement levels %v", got)
	}
}

func (h *harness) ask() {
	h.dictation.Ask(true)
	h.dictation.Ask(false)
}

type shownAnswer struct {
	question, text string
}

type fakeView struct {
	mu       sync.Mutex
	question string
	stop     func()
	opens    int
	updates  int
	done     chan shownAnswer
}

func (v *fakeView) Open(question string, stop func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.question, v.stop = question, stop
	v.opens++
}

func (v *fakeView) Update(text string, done bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.updates++
	if done {
		v.done <- shownAnswer{v.question, text}
	}
}

func (v *fakeView) counts() (opens, updates int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.opens, v.updates
}

func (v *fakeView) close() {
	v.mu.Lock()
	stop := v.stop
	v.mu.Unlock()
	stop()
}

func (h *harness) expectAnswer(t *testing.T) shownAnswer {
	t.Helper()
	select {
	case answer := <-h.view.done:
		return answer
	case <-time.After(time.Second):
		t.Fatal("no answer shown")
		return shownAnswer{}
	}
}

func TestAQuestionIsAnsweredNotTyped(t *testing.T) {
	h := newHarness(t, true)
	h.ask()

	answer := h.expectAnswer(t)
	if answer.question != "hello world" || answer.text != "Paris." {
		t.Fatalf("showed %+v", answer)
	}
	if typed := h.typist.typedSoFar(); len(typed) != 0 {
		t.Fatalf("typed %v, want nothing", typed)
	}
	if remembered := h.typist.remembered(); len(remembered) != 0 {
		t.Fatalf("recorded as speech: %v", remembered)
	}
}

func TestTheIndicatorKeepsOneWaitWhileTheModelAnswers(t *testing.T) {
	h := newHarness(t, false)
	h.ask()

	seen := h.waitOverlay(t, "hide")
	want := []string{"listening", "transcribing", "hide"}
	if !slices.Equal(seen, want) {
		t.Fatalf("indicator went %v, want %v", seen, want)
	}
}

func TestAFailedAnswerIsReportedAsAsk(t *testing.T) {
	h := newHarness(t, false)
	h.typist.answer = ""
	h.typist.askErr = errors.New("provider down")
	h.ask()

	h.expectReport(t, h.typist.askErr)
	if kind := <-h.failed; kind != config.ActionAsk {
		t.Fatalf("reported for %q, want %q", kind, config.ActionAsk)
	}
	if opens, _ := h.view.counts(); opens != 0 {
		t.Fatal("an answer opened for a request that failed before writing")
	}
}

func TestAnotherActionsReleaseDoesNotEndTheTake(t *testing.T) {
	h := newHarness(t, false)
	h.dictation.Ask(true)
	h.dictation.Toggle(false)

	if !h.dictation.Recording(config.ActionAsk) {
		t.Fatal("the ask take ended on the dictate release")
	}
	h.dictation.Ask(false)
	h.expectAnswer(t)
	if questions := h.typist.questions(); len(questions) != 1 {
		t.Fatalf("asked %v, want one question", questions)
	}
}

func TestRecordingClearsAfterAFailedStart(t *testing.T) {
	h := newHarness(t, false)
	h.speech.startErr = errors.New("no microphone")
	h.dictation.Toggle(true)

	h.expectReport(t, h.speech.startErr)
	if h.dictation.Recording(config.ActionDictate) {
		t.Fatal("still recording after the start failed")
	}
}

func TestAnAnswerStreamsIntoOneView(t *testing.T) {
	h := newHarness(t, false)
	h.typist.answer = "Paris is the capital of France."
	h.ask()

	if answer := h.expectAnswer(t); answer.text != "Paris is the capital of France." {
		t.Fatalf("finished with %q", answer.text)
	}
	if opens, updates := h.view.counts(); opens != 1 || updates < 3 {
		t.Fatalf("opened %d times with %d updates, want one view filling up", opens, updates)
	}
}

func TestTheIndicatorGivesWayAtTheFirstWords(t *testing.T) {
	h := newHarness(t, false)
	h.typist.answer = "Paris is the capital."
	h.typist.hold = make(chan struct{})
	h.ask()

	h.waitOverlay(t, "hide")
	if opens, _ := h.view.counts(); opens != 1 {
		t.Fatal("the indicator hid before the answer opened")
	}
	close(h.typist.hold)
	h.expectAnswer(t)
}

func TestClosingTheAnswerCancelsItQuietly(t *testing.T) {
	h := newHarness(t, false)
	h.typist.answer = "Paris is the capital."
	h.typist.hold = make(chan struct{})
	h.ask()

	h.waitUntil(t, "the answer to open", func() bool { opens, _ := h.view.counts(); return opens == 1 })
	h.view.close()

	if answer := h.expectAnswer(t); answer.text != "Paris " {
		t.Fatalf("finished with %q, want the words written so far", answer.text)
	}
	h.expectNoReport(t)
}

func TestAnAnswerCutOffIsKeptAndReported(t *testing.T) {
	h := newHarness(t, false)
	h.typist.answer = "Paris is"
	h.typist.askErr = errors.New("connection lost")
	h.ask()

	if answer := h.expectAnswer(t); answer.text != "Paris is" {
		t.Fatalf("finished with %q", answer.text)
	}
	h.expectReport(t, h.typist.askErr)
}
