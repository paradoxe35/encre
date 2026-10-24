package ducking

import (
	"errors"
	"math"
	"os"
	"path/filepath"
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

func (f *fakeSession) found() any { return map[string]float64{"player": 0.8} }

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

func ducker(t *testing.T, audio *fakeAudio) *Ducker {
	return stepped(t, audio, time.Millisecond)
}

// Closed after each test, so no test inherits another's saved levels.
func stepped(t *testing.T, audio *fakeAudio, step time.Duration) *Ducker {
	d := newDucker(audio.open, step)
	t.Cleanup(d.Close)
	return d
}

// Saved levels go to a scratch directory, never the real app's.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ducking")
	if err != nil {
		panic(err)
	}
	statePath = func() string { return filepath.Join(dir, "ducked-audio.json") }
	pendingPath = func() string { return filepath.Join(dir, "unrestored-audio.json") }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func saved() bool {
	_, err := os.Stat(statePath())
	return err == nil
}

func pendingLeft() bool {
	_, err := os.Stat(pendingPath())
	return err == nil
}

type fakeRestore struct {
	mu    sync.Mutex
	calls []string
	// reply is what is left lowered and the error for each call; nil reaches everything.
	reply func(data string) (string, error)
}

func (f *fakeRestore) restore(data []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, string(data))
	if f.reply == nil {
		return nil, nil
	}
	rest, err := f.reply(string(data))
	if rest == "" {
		return nil, err
	}
	return []byte(rest), err
}

func (f *fakeRestore) restored() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func stubRestore(t *testing.T) *fakeRestore {
	t.Helper()
	fake := &fakeRestore{}
	previous := restoreFound
	restoreFound = fake.restore
	t.Cleanup(func() {
		restoreFound = previous
		os.Remove(statePath())
		os.Remove(pendingPath())
	})
	return fake
}

func writePendingFile(t *testing.T, left ...pending) {
	t.Helper()
	if err := writePending(left); err != nil {
		t.Fatal(err)
	}
}

func writeCrashed(t *testing.T, data string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(statePath(), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statePath(), at, at); err != nil {
		t.Fatal(err)
	}
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
	d := ducker(t, audio)

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
	d := ducker(t, audio)

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
	d := stepped(t, audio, 5*time.Millisecond)

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
	d := stepped(t, audio, 50*time.Millisecond)

	d.Lower()
	waitFor(t, "a session", func() bool { return len(audio.opened()) == 1 })
	d.Close()

	shares, closed := first(audio.opened()).seen()
	if last(shares) != 1 || !closed {
		t.Fatalf("after Close the volume is %v and the session closed is %v", last(shares), closed)
	}
}

func TestCloseWithNothingLoweredReturns(t *testing.T) {
	d := ducker(t, &fakeAudio{})
	d.Close()
}

func TestAFailedOpenLeavesNothingToRestore(t *testing.T) {
	audio := &fakeAudio{fail: errors.New("no sound server")}
	d := ducker(t, audio)

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

func TestLoweringSavesWhatWasFoundAndRestoringForgetsIt(t *testing.T) {
	audio := &fakeAudio{}
	d := ducker(t, audio)

	d.Lower()
	waitFor(t, "the levels to be saved", saved)
	data, err := os.ReadFile(statePath())
	if err != nil || string(data) != `{"player":0.8}` {
		t.Fatalf("saved %q, %v", data, err)
	}
	if info, err := os.Stat(statePath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("saved with %v, %v; want 0600", info.Mode().Perm(), err)
	}

	d.Restore()
	waitFor(t, "the fade up", func() bool { _, closed := first(audio.opened()).seen(); return closed })
	waitFor(t, "the saved levels to be forgotten", func() bool { return !saved() })
}

func TestCloseForgetsTheSavedLevels(t *testing.T) {
	audio := &fakeAudio{}
	d := stepped(t, audio, 50*time.Millisecond)

	d.Lower()
	waitFor(t, "the levels to be saved", saved)
	d.Close()
	if saved() {
		t.Fatal("the saved levels outlived Close")
	}
}

func TestRecoverStalePutsBackWhatACrashLeftLowered(t *testing.T) {
	fake := stubRestore(t)
	writeCrashed(t, `{"player":0.8}`, time.Now())

	if err := RecoverStale(); err != nil {
		t.Fatal(err)
	}
	if got := fake.restored(); len(got) != 1 || got[0] != `{"player":0.8}` {
		t.Fatalf("restored %v", got)
	}
	if saved() || pendingLeft() {
		t.Fatalf("after a full recovery saved is %v and pending is %v", saved(), pendingLeft())
	}
}

func TestRecoverStaleWithNothingSavedDoesNothing(t *testing.T) {
	fake := stubRestore(t)
	if err := RecoverStale(); err != nil {
		t.Fatal(err)
	}
	if got := fake.restored(); len(got) != 0 {
		t.Fatalf("restored %v with nothing saved", got)
	}
}

// A duck this run is still holding is not a crash's leftover.
func TestRecoverStaleLeavesALiveDuckAlone(t *testing.T) {
	fake := stubRestore(t)
	audio := &fakeAudio{}
	d := ducker(t, audio)
	d.Lower()
	waitFor(t, "the levels to be saved", saved)

	if err := RecoverStale(); err != nil {
		t.Fatal(err)
	}
	if got := fake.restored(); len(got) != 0 || !saved() {
		t.Fatalf("recovered a live duck: restored %v, saved %v", got, saved())
	}
	d.Close()
}

// After a reboot the player may not be open yet: what was not reached is kept and tried again.
func TestRecoverStaleKeepsWhatItCouldNotReachForLater(t *testing.T) {
	fake := stubRestore(t)
	fake.reply = func(string) (string, error) { return `["spotify"]`, nil }
	writeCrashed(t, `["firefox","spotify"]`, time.Now())

	if err := RecoverStale(); err != nil {
		t.Fatal(err)
	}
	if saved() || !pendingLeft() {
		t.Fatalf("after a partial recovery saved is %v and pending is %v", saved(), pendingLeft())
	}

	fake.reply = nil
	if err := RecoverStale(); err != nil {
		t.Fatal(err)
	}
	if got := fake.restored(); len(got) != 2 || got[1] != `["spotify"]` {
		t.Fatalf("restored %v, want only what was left tried again", got)
	}
	if pendingLeft() {
		t.Fatal("the pending levels outlived their restore")
	}
}

func TestRecoverStaleKeepsEverythingWhenTheSoundServerIsNotUp(t *testing.T) {
	fake := stubRestore(t)
	fake.reply = func(data string) (string, error) { return data, errors.New("no sound server") }
	writeCrashed(t, `["spotify"]`, time.Now())

	if err := RecoverStale(); err == nil {
		t.Fatal("the failure was not reported")
	}
	left, err := readPending()
	if err != nil || len(left) != 1 || string(left[0].Found) != `["spotify"]` {
		t.Fatalf("pending %v, %v", left, err)
	}
	if saved() {
		t.Fatal("the crash's levels were kept twice")
	}
}

// Nothing in levels that do not parse can be restored, so they are not kept.
func TestRecoverStaleDropsLevelsThatDoNotParse(t *testing.T) {
	fake := stubRestore(t)
	fake.reply = func(string) (string, error) { return "", errors.New("unexpected end of JSON input") }
	writeCrashed(t, `["spot`, time.Now())

	if err := RecoverStale(); err == nil {
		t.Fatal("the failure was not reported")
	}
	if saved() || pendingLeft() {
		t.Fatalf("unparseable levels kept: saved %v, pending %v", saved(), pendingLeft())
	}
}

func TestRecoverStaleDropsAnUnreadablePendingFile(t *testing.T) {
	fake := stubRestore(t)
	if err := os.WriteFile(pendingPath(), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverStale(); err != nil {
		t.Fatal(err)
	}
	if got := fake.restored(); len(got) != 0 || pendingLeft() {
		t.Fatalf("restored %v, pending kept %v", got, pendingLeft())
	}
}

// A week on, the app is surely back at a level someone chose: it is let be.
func TestRecoverStaleGivesUpOnOldLevels(t *testing.T) {
	fake := stubRestore(t)
	old := time.Now().Add(-keepPending - time.Hour)
	writePendingFile(t,
		pending{Saved: old, Found: []byte(`["old"]`)},
		pending{Saved: time.Now(), Found: []byte(`["recent"]`)},
	)
	writeCrashed(t, `["crashed long ago"]`, old)

	if err := RecoverStale(); err != nil {
		t.Fatal(err)
	}
	if got := fake.restored(); len(got) != 1 || got[0] != `["recent"]` {
		t.Fatalf("restored %v, want only the recent levels", got)
	}
	if saved() || pendingLeft() {
		t.Fatalf("old levels kept: saved %v, pending %v", saved(), pendingLeft())
	}
}

func TestLoweringFirstRetriesWhatWasLeftLowered(t *testing.T) {
	fake := stubRestore(t)
	audio := &fakeAudio{}
	var openAtRetry []int
	fake.reply = func(string) (string, error) {
		openAtRetry = append(openAtRetry, len(audio.opened()))
		return "", nil
	}
	writePendingFile(t, pending{Saved: time.Now(), Found: []byte(`["spotify"]`)})
	d := ducker(t, audio)

	d.Lower()
	waitFor(t, "the levels to be saved", saved)
	if got := fake.restored(); len(got) != 1 || got[0] != `["spotify"]` {
		t.Fatalf("restored %v before lowering", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(openAtRetry) != 1 || openAtRetry[0] != 0 {
		t.Fatalf("retried with %v sessions open, want it done before opening", openAtRetry)
	}
	if pendingLeft() {
		t.Fatal("the pending levels outlived their retry")
	}
}

func TestLoweringWithNothingPendingRestoresNothing(t *testing.T) {
	fake := stubRestore(t)
	d := ducker(t, &fakeAudio{})
	d.Lower()
	waitFor(t, "the levels to be saved", saved)
	if got := fake.restored(); len(got) != 0 {
		t.Fatalf("restored %v with nothing pending", got)
	}
}

func TestStillDuckedOnlyNearTheDuckedLevel(t *testing.T) {
	for _, share := range []float64{gain, gain * 1.2, gain / 1.2} {
		if !stillDucked(share) {
			t.Errorf("share %v counted as set since", share)
		}
	}
	for _, share := range []float64{0, gain / 2, gain * 2, 0.5, 1, 1.5} {
		if stillDucked(share) {
			t.Errorf("share %v counted as still ducked", share)
		}
	}
}
