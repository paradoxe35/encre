package main

import (
	"sync/atomic"
	"testing"
	"time"
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
