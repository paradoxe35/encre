package main

import (
	"sync"
	"testing"
	"time"
)

func TestBuild(t *testing.T) {
	t.Log("Application compiles successfully")
}

// Listeners run on their own goroutine, so two saves close together reload at
// once. Interleaved, one call's clear would drop what the other just registered.
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

// Every save reloads: none is deferred or dropped, so a shortcut enabled in one
// is bound by the time it returns.
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
