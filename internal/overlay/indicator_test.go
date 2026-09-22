package overlay

import (
	"errors"
	"image"
	"sync"
	"testing"
	"time"
)

// fakeSurface records what reached the window and advances a fake clock per frame.
type fakeSurface struct {
	mu       sync.Mutex
	clock    *time.Time
	alphas   []uint8
	closed   bool
	presents int
}

func (f *fakeSurface) Scale() float64 { return 1 }

func (f *fakeSurface) Present(frame *image.RGBA) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.presents++
	f.alphas = append(f.alphas, alphaAt(frame, 20, Height/2))
	*f.clock = f.clock.Add(frameInterval)
}

func (f *fakeSurface) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *fakeSurface) state() (alphas []uint8, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint8(nil), f.alphas...), f.closed
}

type bench struct {
	indicator *Indicator
	owner     *Owner
	surfaces  []*fakeSurface
	clock     time.Time
	mu        sync.Mutex
	openErr   error
	mainCalls int
}

func newBench(t *testing.T) *bench {
	t.Helper()
	b := &bench{clock: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	runOnMain := func(f func()) {
		b.mu.Lock()
		b.mainCalls++
		b.mu.Unlock()
		f()
	}
	open := func() (surface, error) {
		if b.openErr != nil {
			return nil, b.openErr
		}
		s := &fakeSurface{clock: &b.clock}
		b.mu.Lock()
		b.surfaces = append(b.surfaces, s)
		b.mu.Unlock()
		return s, nil
	}
	b.indicator = newIndicator(runOnMain, open, func() time.Time { return b.clock }, time.Millisecond)
	b.owner = b.indicator.Owner()
	return b
}

func (b *bench) surface(t *testing.T, n int) *fakeSurface {
	t.Helper()
	b.waitUntil(t, "a surface", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.surfaces) > n
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.surfaces[n]
}

func (b *bench) waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (b *bench) running() bool {
	b.indicator.mu.Lock()
	defer b.indicator.mu.Unlock()
	return b.indicator.running
}

func TestShowFadesInToFullStrength(t *testing.T) {
	b := newBench(t)
	b.owner.Show(Listening)
	s := b.surface(t, 0)

	b.waitUntil(t, "the fade-in", func() bool {
		alphas, _ := s.state()
		return len(alphas) > 0 && alphas[len(alphas)-1] >= 220
	})
	alphas, closed := s.state()
	if closed {
		t.Fatal("the surface was closed while still shown")
	}
	if alphas[0] >= alphas[len(alphas)-1] {
		t.Fatalf("no fade: first frame %d, last %d", alphas[0], alphas[len(alphas)-1])
	}
}

func TestHideFadesOutThenClosesAndStops(t *testing.T) {
	b := newBench(t)
	b.owner.Show(Listening)
	s := b.surface(t, 0)
	b.waitUntil(t, "the fade-in", func() bool {
		alphas, _ := s.state()
		return len(alphas) > 0 && alphas[len(alphas)-1] >= 220
	})

	b.owner.Hide()
	b.waitUntil(t, "the close", func() bool { _, closed := s.state(); return closed })
	b.waitUntil(t, "the goroutine to end", func() bool { return !b.running() })

	alphas, _ := s.state()
	if alphas[len(alphas)-1] != 0 {
		t.Fatalf("last frame before closing had alpha %d, want a full fade-out", alphas[len(alphas)-1])
	}
}

func TestShowDuringTheFadeOutBringsItBack(t *testing.T) {
	b := newBench(t)
	b.owner.Show(Listening)
	s := b.surface(t, 0)
	b.waitUntil(t, "the fade-in", func() bool {
		alphas, _ := s.state()
		return len(alphas) > 0 && alphas[len(alphas)-1] >= 220
	})

	b.owner.Hide()
	b.waitUntil(t, "the fade-out to begin", func() bool {
		alphas, _ := s.state()
		return alphas[len(alphas)-1] < 200
	})
	b.owner.Show(Transcribing)

	b.waitUntil(t, "the fade back in", func() bool {
		alphas, closed := s.state()
		return !closed && alphas[len(alphas)-1] >= 220
	})
}

func TestAShowAfterAFinishedHideOpensAFreshSurface(t *testing.T) {
	b := newBench(t)
	b.owner.Show(Listening)
	first := b.surface(t, 0)
	b.owner.Hide()
	b.waitUntil(t, "the first close", func() bool { _, closed := first.state(); return closed })
	b.waitUntil(t, "idle", func() bool { return !b.running() })

	b.owner.Show(Listening)
	second := b.surface(t, 1)
	if second == first {
		t.Fatal("the closed surface was reused")
	}
	b.owner.Hide()
	b.waitUntil(t, "the second close", func() bool { _, closed := second.state(); return closed })
}

func TestAnUnavailableSurfaceGoesQuiet(t *testing.T) {
	b := newBench(t)
	b.openErr = errors.New("no floating windows here")
	b.owner.Show(Listening)
	b.waitUntil(t, "the attempt to end", func() bool { return !b.running() })

	b.owner.Show(Transcribing)
	b.indicator.Level(0.5)
	b.owner.Hide()
	time.Sleep(5 * time.Millisecond)
	if b.running() || len(b.surfaces) != 0 {
		t.Fatal("the indicator kept trying after the platform refused")
	}

	b.openErr = nil
	b.clock = b.clock.Add(retryAfter)
	b.owner.Show(Listening)
	b.surface(t, 0)
	b.owner.Hide()
}

func TestEverySurfaceCallRunsOnTheMainThread(t *testing.T) {
	b := newBench(t)
	b.owner.Show(Listening)
	s := b.surface(t, 0)
	b.owner.Hide()
	b.waitUntil(t, "the close", func() bool { _, closed := s.state(); return closed })
	b.waitUntil(t, "idle", func() bool { return !b.running() })

	alphas, _ := s.state()
	b.mu.Lock()
	defer b.mu.Unlock()
	if want := len(alphas) + 2; b.mainCalls != want {
		t.Fatalf("%d main-thread calls for open, %d frames and close, want %d", b.mainCalls, len(alphas), want)
	}
}

func TestLevelIsClampedAndHarmlessWhenHidden(t *testing.T) {
	b := newBench(t)
	b.indicator.Level(-3)
	b.owner.Hide()

	b.indicator.mu.Lock()
	level := b.indicator.level
	b.indicator.mu.Unlock()
	if level != 0 {
		t.Fatalf("level %v after a negative update, want 0", level)
	}
	b.indicator.Level(7)
	b.indicator.mu.Lock()
	level = b.indicator.level
	b.indicator.mu.Unlock()
	if level != 1 {
		t.Fatalf("level %v after an over-range update, want 1", level)
	}
}

func TestLoudnessMakesSpeechVisible(t *testing.T) {
	if l := loudness(0); l != 0 {
		t.Fatalf("silence reads %v", l)
	}
	if l := loudness(0.002); l != 0 {
		t.Fatalf("room noise reads %v, want 0", l)
	}
	quiet, normal, loud := loudness(0.01), loudness(0.05), loudness(0.3)
	if !(quiet < normal && normal < loud) {
		t.Fatalf("not monotonic: quiet %v, normal %v, loud %v", quiet, normal, loud)
	}
	if normal < 0.4 || normal > 0.7 {
		t.Fatalf("normal speech reads %v, want somewhere in the middle", normal)
	}
	if loud != 1 {
		t.Fatalf("loud speech reads %v, want the top", loud)
	}
}

func TestTheTraceShiftsTowardsTheOldestSlot(t *testing.T) {
	var trace [traceLen]float32
	trace = advance(trace, 1)
	trace = advance(trace, 1)
	trace = advance(trace, 0)
	if trace[traceLen-1] >= trace[traceLen-2] {
		t.Fatalf("trace %v: the newest slot should have started falling", trace)
	}
	if trace[0] != 0 {
		t.Fatalf("trace %v: the oldest slot should still be empty", trace)
	}
	for range traceLen {
		trace = advance(trace, 1)
	}
	if trace[0] == 0 {
		t.Fatalf("trace %v: levels never reached the oldest slot", trace)
	}
}

func TestTheLevelMeterRisesFastAndSettlesSlowly(t *testing.T) {
	up := follow(0, 1)
	down := follow(1, 0)
	if up < 0.5 {
		t.Fatalf("attack step %v, want most of the way up", up)
	}
	if 1-down > up {
		t.Fatalf("release step %v is faster than the attack %v", 1-down, up)
	}
}

func TestFadeIsBoundedAndTimed(t *testing.T) {
	if a := fade(0, true, fadeIn); a != 1 {
		t.Fatalf("a full fade-in interval reached %v", a)
	}
	if a := fade(1, false, fadeOut); a != 0 {
		t.Fatalf("a full fade-out interval reached %v", a)
	}
	if a := fade(1, true, time.Second); a != 1 {
		t.Fatalf("fade-in overshot to %v", a)
	}
	if a := fade(0, false, time.Second); a != 0 {
		t.Fatalf("fade-out undershot to %v", a)
	}
}

func TestOneOwnerEndingDoesNotHideAnother(t *testing.T) {
	b := newBench(t)
	dictation := b.indicator.Owner()
	actions := b.indicator.Owner()

	dictation.Show(Listening)
	s := b.surface(t, 0)
	actions.Show(Thinking)
	actions.Hide()

	time.Sleep(20 * time.Millisecond)
	if _, closed := s.state(); closed {
		t.Fatal("the actions owner hiding closed the dictation owner's window")
	}
	b.indicator.mu.Lock()
	phase := b.indicator.phase
	b.indicator.mu.Unlock()
	if phase != Listening {
		t.Fatalf("phase %v after the other owner left, want the dictation phase back", phase)
	}

	dictation.Hide()
	b.waitUntil(t, "the close", func() bool { _, closed := s.state(); return closed })
}

func TestTheLastShowSetsThePhase(t *testing.T) {
	b := newBench(t)
	first := b.indicator.Owner()
	second := b.indicator.Owner()
	first.Show(Listening)
	second.Show(Thinking)

	b.indicator.mu.Lock()
	phase := b.indicator.phase
	b.indicator.mu.Unlock()
	if phase != Thinking {
		t.Fatalf("phase %v, want the newest show", phase)
	}
	first.Hide()
	second.Hide()
}

func TestCloseTakesTheWindowDownAtOnceAndForGood(t *testing.T) {
	b := newBench(t)
	b.owner.Show(Listening)
	s := b.surface(t, 0)
	b.waitUntil(t, "the fade-in", func() bool {
		alphas, _ := s.state()
		return len(alphas) > 0 && alphas[len(alphas)-1] >= 220
	})

	b.indicator.Close()
	if _, closed := s.state(); !closed {
		t.Fatal("Close returned before the window was gone")
	}
	if b.running() {
		t.Fatal("the animation outlived Close")
	}

	b.owner.Show(Listening)
	time.Sleep(5 * time.Millisecond)
	if len(b.surfaces) != 1 {
		t.Fatal("a show after Close opened a window")
	}
}

func TestCloseWithoutAWindowReturnsAtOnce(t *testing.T) {
	b := newBench(t)
	b.indicator.Close()
}

func TestAShowDuringTheFadeOutIsHonouredWithoutForcingIt(t *testing.T) {
	b := newBench(t)
	b.owner.Show(Listening)
	s := b.surface(t, 0)
	b.owner.Hide()
	b.waitUntil(t, "a frame", func() bool {
		alphas, _ := s.state()
		return len(alphas) >= 1
	})

	// Show then Hide in quick succession must not leave any window up.
	b.owner.Show(Listening)
	b.owner.Hide()
	b.waitUntil(t, "idle", func() bool { return !b.running() })
	time.Sleep(10 * time.Millisecond)
	if b.running() {
		t.Fatal("still running after a show and hide during the fade-out")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for n, surface := range b.surfaces {
		if _, closed := surface.state(); !closed {
			t.Fatalf("surface %d was left open", n)
		}
	}
}
