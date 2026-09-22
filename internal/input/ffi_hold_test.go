//go:build linux || darwin || windows

package input

import (
	"sync"
	"testing"
	"time"
)

type edgeLog struct {
	mu    sync.Mutex
	edges []bool
}

func (e *edgeLog) record(down bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.edges = append(e.edges, down)
}

func (e *edgeLog) seen() []bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]bool(nil), e.edges...)
}

func bind(t *testing.T, action string) *edgeLog {
	t.Helper()
	log := &edgeLog{}

	holdMu.Lock()
	holdBindings[action] = &holdBinding{handler: log.record}
	holdMu.Unlock()

	t.Cleanup(func() {
		holdMu.Lock()
		delete(holdBindings, action)
		holdMu.Unlock()
	})
	return log
}

// settle waits past the release grace plus the goroutine the handler runs on.
func settle() { time.Sleep(releaseGrace + 60*time.Millisecond) }

func TestHoldReportsBothEdges(t *testing.T) {
	log := bind(t, "dictate")

	dispatchHoldDown("dictate")
	time.Sleep(20 * time.Millisecond)
	dispatchHoldUp("dictate")
	settle()

	edges := log.seen()
	if len(edges) != 2 || !edges[0] || edges[1] {
		t.Fatalf("expected one down then one up, got %v", edges)
	}
}

// X11 repeats a held key as release/press pairs; a release cancelled inside the grace window must not reach the handler.
func TestHoldIgnoresAutoRepeat(t *testing.T) {
	log := bind(t, "dictate")

	dispatchHoldDown("dictate")
	for range 5 {
		time.Sleep(10 * time.Millisecond)
		dispatchHoldUp("dictate")
		time.Sleep(10 * time.Millisecond)
		dispatchHoldDown("dictate")
	}

	if edges := log.seen(); len(edges) != 1 || !edges[0] {
		t.Fatalf("auto-repeat leaked through: %v", edges)
	}

	dispatchHoldUp("dictate")
	settle()

	edges := log.seen()
	if len(edges) != 2 || edges[1] {
		t.Fatalf("expected a single up after the repeats, got %v", edges)
	}
}

func TestHoldIgnoresRepeatedDown(t *testing.T) {
	log := bind(t, "dictate")

	for range 4 {
		dispatchHoldDown("dictate")
	}
	time.Sleep(20 * time.Millisecond)

	if edges := log.seen(); len(edges) != 1 {
		t.Fatalf("a held key should report down once, got %v", edges)
	}
}

// A release with no matching press would otherwise stop a recording that never started.
func TestHoldIgnoresUpWithoutDown(t *testing.T) {
	log := bind(t, "dictate")

	dispatchHoldUp("dictate")
	settle()

	if edges := log.seen(); len(edges) != 0 {
		t.Fatalf("expected no edges, got %v", edges)
	}
}

func TestHoldIgnoresUnknownAction(t *testing.T) {
	bind(t, "dictate")

	dispatchHoldDown("nothing-bound-here")
	dispatchHoldUp("nothing-bound-here")
	settle()
}

func TestClearHoldBindingsStopsPendingRelease(t *testing.T) {
	log := bind(t, "dictate")

	dispatchHoldDown("dictate")
	time.Sleep(10 * time.Millisecond)
	dispatchHoldUp("dictate")

	ClearHoldBindings()
	settle()

	edges := log.seen()
	if len(edges) != 1 || !edges[0] {
		t.Fatalf("clearing should cancel the deferred release, got %v", edges)
	}
}

func TestReRegisteringWhileHeldKeepsTheHold(t *testing.T) {
	old := bind(t, "dictate")
	dispatchHoldDown("dictate")
	time.Sleep(20 * time.Millisecond)

	replacement := &edgeLog{}
	rememberHold("dictate", replacement.record)
	dispatchHoldUp("dictate")
	settle()

	if edges := old.seen(); len(edges) != 1 || !edges[0] {
		t.Fatalf("old handler saw %v, want only the down edge", edges)
	}
	if edges := replacement.seen(); len(edges) != 1 || edges[0] {
		t.Fatalf("replacement saw %v, want only the up edge", edges)
	}
}

func TestForgettingAHeldBindingReleasesIt(t *testing.T) {
	log := bind(t, "dictate")
	dispatchHoldDown("dictate")
	time.Sleep(20 * time.Millisecond)

	forgetHold("dictate")
	settle()

	if edges := log.seen(); len(edges) != 2 || !edges[0] || edges[1] {
		t.Fatalf("saw %v, want a down then the synthesised up", edges)
	}
	holdMu.Lock()
	_, still := holdBindings["dictate"]
	holdMu.Unlock()
	if still {
		t.Fatal("the binding was not removed")
	}
}

func TestForgettingAnIdleBindingIsSilent(t *testing.T) {
	log := bind(t, "dictate")
	forgetHold("dictate")
	settle()
	if edges := log.seen(); len(edges) != 0 {
		t.Fatalf("saw %v for a binding that was never pressed", edges)
	}
}
