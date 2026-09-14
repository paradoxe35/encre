package revision

import (
	"sync"
	"testing"
	"time"
)

func TestSequenceTypesInOrderSpoken(t *testing.T) {
	var order sequence
	var mu sync.Mutex
	var typed []int

	// Later takes finish transcribing first; they must still be typed last.
	delays := []time.Duration{60 * time.Millisecond, 30 * time.Millisecond, 0}

	var wg sync.WaitGroup
	for i, delay := range delays {
		ahead, done := order.claim()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer done()

			time.Sleep(delay)
			if ahead != nil {
				<-ahead
			}

			mu.Lock()
			typed = append(typed, i)
			mu.Unlock()
		}()
	}
	wg.Wait()

	for i, got := range typed {
		if got != i {
			t.Fatalf("typed %v, want them in the order spoken", typed)
		}
	}
}

// The first take has nothing ahead of it and must not wait.
func TestSequenceFirstClaimHasNoGate(t *testing.T) {
	var order sequence

	ahead, done := order.claim()
	if ahead != nil {
		t.Error("the first take was given a gate to wait on")
	}
	done()

	next, _ := order.claim()
	if next == nil {
		t.Fatal("the second take was given no gate")
	}
	select {
	case <-next:
	case <-time.After(time.Second):
		t.Error("the gate never opened after the take ahead finished")
	}
}

// A take that fails still has to release the one behind it.
func TestSequenceReleasesOnFailure(t *testing.T) {
	var order sequence

	_, first := order.claim()
	ahead, _ := order.claim()

	go func() { first() }()

	select {
	case <-ahead:
	case <-time.After(time.Second):
		t.Fatal("a failed take left the next one waiting forever")
	}
}

func TestSequenceUnderConcurrentClaims(t *testing.T) {
	var order sequence

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ahead, done := order.claim()
			if ahead != nil {
				<-ahead
			}
			done()
		}()
	}

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("claims deadlocked")
	}
}
