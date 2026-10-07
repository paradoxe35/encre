package ducking

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	mu     sync.Mutex
	shares []float64
	closed bool
}

func (f *fakeSession) scale(share float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shares = append(f.shares, share)
	return nil
}

func (f *fakeSession) current() float64 {
	shares, _ := f.seen()
	return last(shares)
}

func (f *fakeSession) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *fakeSession) seen() ([]float64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]float64(nil), f.shares...), f.closed
}

type fakeAudio struct {
	mu       sync.Mutex
	sessions []*fakeSession
	fail     error
}

func (f *fakeAudio) open() (session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	s := &fakeSession{}
	f.sessions = append(f.sessions, s)
	return s, nil
}

func (f *fakeAudio) opened() []*fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeSession(nil), f.sessions...)
}

func ducker(audio *fakeAudio) *Ducker {
	return newDucker(audio.open, time.Millisecond)
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func last(shares []float64) float64 {
	if len(shares) == 0 {
		return 1
	}
	return shares[len(shares)-1]
}

func TestLoweringFadesDownInManyStepsToTheDrop(t *testing.T) {
	audio := &fakeAudio{}
	d := ducker(audio)

	d.Lower()
	waitFor(t, "the fade down", func() bool {
		s := audio.opened()
		return len(s) == 1 && math.Abs(first(s).current()-gain) < 1e-9
	})

	shares, _ := first(audio.opened()).seen()
	if len(shares) < 10 {
		t.Fatalf("faded in %d steps, want a gradual fade", len(shares))
	}
	for i := 1; i < len(shares); i++ {
		if shares[i] > shares[i-1] {
			t.Fatalf("the volume rose during a fade down: %v", shares)
		}
	}
}

func TestRestoringFadesBackUpAndLetsGo(t *testing.T) {
	audio := &fakeAudio{}
	d := ducker(audio)

	d.Lower()
	waitFor(t, "the fade down", func() bool { return len(audio.opened()) == 1 && first(audio.opened()).current() == gain })
	d.Restore()
	waitFor(t, "the fade up", func() bool { _, closed := first(audio.opened()).seen(); return closed })

	shares, _ := first(audio.opened()).seen()
	if last(shares) != 1 {
		t.Fatalf("restored to %v, want the volume found", last(shares))
	}
}

// A take that starts while the music is coming back must turn it around, not start from scratch.
func TestAWishMidFadeTurnsItAround(t *testing.T) {
	audio := &fakeAudio{}
	d := newDucker(audio.open, 5*time.Millisecond)

	d.Lower()
	waitFor(t, "the fade down", func() bool { return len(audio.opened()) == 1 && first(audio.opened()).current() == gain })
	d.Restore()
	waitFor(t, "the fade up to begin", func() bool { return first(audio.opened()).current() > gain })
	d.Lower()
	waitFor(t, "the turn back down", func() bool { return first(audio.opened()).current() == gain })

	if n := len(audio.opened()); n != 1 {
		t.Fatalf("opened %d sessions, want the one being faded", n)
	}
	if _, closed := first(audio.opened()).seen(); closed {
		t.Fatal("the session closed while still lowered")
	}
}

func TestCloseRestoresAtOnceAndWaits(t *testing.T) {
	audio := &fakeAudio{}
	d := newDucker(audio.open, 50*time.Millisecond)

	d.Lower()
	waitFor(t, "a session", func() bool { return len(audio.opened()) == 1 })
	d.Close()

	shares, closed := first(audio.opened()).seen()
	if last(shares) != 1 || !closed {
		t.Fatalf("after Close the volume is %v and the session closed is %v", last(shares), closed)
	}
}

func TestCloseWithNothingLoweredReturns(t *testing.T) {
	d := ducker(&fakeAudio{})
	d.Close()
}

func TestAFailedOpenLeavesNothingToRestore(t *testing.T) {
	audio := &fakeAudio{fail: errors.New("no sound server")}
	d := ducker(audio)

	d.Lower()
	d.Close()
	if len(audio.opened()) != 0 {
		t.Fatal("a session appeared after a failed open")
	}
}

func TestTheFadeIsEvenInDecibelsAndEasedAtTheEnds(t *testing.T) {
	db := func(share float64) float64 { return 20 * math.Log10(share) }
	if got := db(between(1, gain, 0.5)); math.Abs(got+9) > 0.01 {
		t.Fatalf("halfway is %.2f dB, want -9", got)
	}
	start, end := db(between(1, gain, 0.05)), db(between(1, gain, 0.95))
	if start < -0.2 || end > -17.8 {
		t.Fatalf("the ends move too fast: %.2f dB at 5%%, %.2f dB at 95%%", start, end)
	}
}

func first(sessions []*fakeSession) *fakeSession { return sessions[0] }
