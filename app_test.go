package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/overlay"
)

// fakeMain is one UI thread that runs queued work in order, as Fyne's does.
type fakeMain struct {
	queue chan func()
}

func newFakeMain(t *testing.T) *fakeMain {
	m := &fakeMain{queue: make(chan func(), 16)}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case f := <-m.queue:
				f()
			case <-stop:
				return
			}
		}
	}()
	return m
}

func (m *fakeMain) do(f func()) { m.queue <- f }

func (m *fakeMain) doAndWait(f func()) {
	done := make(chan struct{})
	m.queue <- func() { f(); close(done) }
	<-done
}

// After a signal or Cmd+Q the loop is gone, and closing a showing indicator would wait on it forever.
func TestClosingTheOverlayAfterTheLoopDoesNotWait(t *testing.T) {
	deadLoop := func(func()) { select {} }
	app := &Application{indicator: overlay.New(deadLoop)}
	app.indicator.Owner().Show(overlay.Thinking)
	app.loopEnded.Store(true)

	closed := make(chan struct{})
	go func() {
		app.closeOverlay()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the overlay waited on a loop that had ended")
	}
}

// Fyne runs work handed to it after its loop on the caller, where GLFW is already terminated.
func TestTheOverlayRunsNothingOnMainAfterTheLoop(t *testing.T) {
	app := &Application{}
	app.loopEnded.Store(true)
	app.onMain(func() { t.Fatal("ran on a loop that had ended") })
}

// The tray runs Quit on the UI thread, and closing the indicator needs that thread.
func TestTrayQuitDoesNotWaitOnTheUIThread(t *testing.T) {
	main := newFakeMain(t)
	var windowClosed atomic.Bool
	quitWithWindowGone := make(chan bool, 1)

	teardown := func() {
		main.doAndWait(func() { windowClosed.Store(true) })
	}
	quit := func() {
		main.do(func() { quitWithWindowGone <- windowClosed.Load() })
	}

	trayReturned := make(chan struct{})
	main.do(func() {
		stopOffMain(teardown, quit)
		close(trayReturned)
	})

	select {
	case <-trayReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("the tray's Quit blocked the UI thread")
	}
	select {
	case gone := <-quitWithWindowGone:
		if !gone {
			t.Fatal("quit before the indicator window was closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the app never quit")
	}
}
