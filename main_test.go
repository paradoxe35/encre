package main

import (
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestBuild(t *testing.T) {
	t.Log("Application compiles successfully")
}

// Concurrent reloads must not let one's clear drop what the other just registered.
func TestReloadIsSerialised(t *testing.T) {
	app := &Application{}
	app.reloadMutex.Lock()

	ran := make(chan struct{})
	go func() {
		app.reloadHotkeysFromConfig()
		close(ran)
	}()

	select {
	case <-ran:
		t.Fatal("a reload ran while another held the lock")
	case <-time.After(50 * time.Millisecond):
	}

	app.reloadMutex.Unlock()

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the reload never ran once the lock was free")
	}
}

// A shortcut enabled in a save is bound by the time it returns.
func TestConcurrentReloadsAreSafe(t *testing.T) {
	app := &Application{}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.reloadHotkeysFromConfig()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reloads deadlocked")
	}
}

// Fyne only catches SIGINT and SIGTERM once its loop runs; one arriving earlier must still stop the app.
func TestSignalsStopTheAppWhoeverIsListening(t *testing.T) {
	cases := []struct {
		signal      os.Signal
		loopStarted bool
		stop, quit  bool
	}{
		{syscall.SIGHUP, true, true, false},
		{syscall.SIGHUP, false, false, true},
		{syscall.SIGTERM, true, false, false},
		{os.Interrupt, true, false, false},
		{syscall.SIGTERM, false, false, true},
		{os.Interrupt, false, false, true},
	}
	for _, c := range cases {
		var stopped, quit bool
		onSignal(c.signal, c.loopStarted, func() { stopped = true }, func() { quit = true })
		if stopped != c.stop || quit != c.quit {
			t.Errorf("%v with the loop started %v: stopped %v, quit %v", c.signal, c.loopStarted, stopped, quit)
		}
	}
}
