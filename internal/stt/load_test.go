package stt

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/config"
)

type stubLoader struct {
	took    time.Duration
	err     error
	started chan struct{}
	once    sync.Once
}

func (l *stubLoader) Load(string) error {
	l.once.Do(func() { close(l.started) })
	time.Sleep(l.took)
	return l.err
}

func newStubLoader(took time.Duration, err error) *stubLoader {
	return &stubLoader{took: took, err: err, started: make(chan struct{})}
}

// Capture has to begin without waiting for the model, so the load must not block
// the caller that starts it.
func TestBeginLoadDoesNotBlock(t *testing.T) {
	service := NewService()
	loader := newStubLoader(300*time.Millisecond, nil)

	returned := make(chan struct{})
	go func() {
		service.mu.Lock()
		service.beginLoad(loader, "/models/whisper.gguf")
		service.mu.Unlock()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("beginLoad waited for the model; the first words would be lost")
	}

	<-loader.started
	if err := service.awaitLoad(); err != nil {
		t.Fatal(err)
	}
	if service.loaded != "/models/whisper.gguf" {
		t.Errorf("loaded = %q, want the model marked resident", service.loaded)
	}
}

func TestAwaitLoadReturnsTheFailure(t *testing.T) {
	service := NewService()
	failure := errors.New("no such file")

	service.mu.Lock()
	service.beginLoad(newStubLoader(0, failure), "/models/gone.gguf")
	service.mu.Unlock()

	if err := service.awaitLoad(); !errors.Is(err, failure) {
		t.Fatalf("awaitLoad = %v, want %v", err, failure)
	}
	if service.loaded != "" {
		t.Errorf("loaded = %q; a failed load leaves the engine empty", service.loaded)
	}
}

// Nothing was started, so there is nothing to wait for.
func TestAwaitLoadWithNothingPending(t *testing.T) {
	service := NewService()

	done := make(chan error, 1)
	go func() { done <- service.awaitLoad() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("awaitLoad = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("awaitLoad blocked with no load in flight")
	}
}

// The second take must not wait on the first take's load a second time.
func TestAwaitLoadClearsThePending(t *testing.T) {
	service := NewService()

	service.mu.Lock()
	service.beginLoad(newStubLoader(0, nil), "/models/whisper.gguf")
	service.mu.Unlock()

	if err := service.awaitLoad(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- service.awaitLoad() }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the second await blocked on a load that had already finished")
	}
}

// Asking again for the model already loading is the same request, not a new one.
func TestBeginLoadIgnoresARepeatOfTheSameModel(t *testing.T) {
	service := NewService()

	service.mu.Lock()
	service.beginLoad(newStubLoader(100*time.Millisecond, nil), "/models/first.gguf")
	pending := service.pendingLoad
	service.beginLoad(newStubLoader(0, errors.New("should not run")), "/models/first.gguf")
	kept := service.pendingLoad
	service.mu.Unlock()

	if pending != kept {
		t.Error("a repeat replaced the load the take is waiting on")
	}
	if err := service.awaitLoad(); err != nil {
		t.Fatal(err)
	}
}

// Switching model before the first take must not be transcribed with the model
// the startup preload happened to pick.
func TestBeginLoadSupersedesADifferentModel(t *testing.T) {
	service := NewService()
	wanted := newStubLoader(0, nil)

	service.mu.Lock()
	service.beginLoad(newStubLoader(200*time.Millisecond, nil), "/models/preloaded.gguf")
	service.beginLoad(wanted, "/models/chosen.gguf")
	service.mu.Unlock()

	select {
	case <-wanted.started:
	case <-time.After(time.Second):
		t.Fatal("the chosen model never loaded")
	}
	if err := service.awaitLoad(); err != nil {
		t.Fatal(err)
	}
	if service.loaded != "/models/chosen.gguf" {
		t.Errorf("loaded = %q, want the chosen model", service.loaded)
	}
}

func TestModelPathRefusesWhatCannotBeLoaded(t *testing.T) {
	service := NewService()

	if _, err := service.modelPath(config.SpeechConfig{}); !errors.Is(err, ErrNoModel) {
		t.Errorf("no model selected = %v, want ErrNoModel", err)
	}
	if _, err := service.modelPath(config.SpeechConfig{ModelID: "not-in-the-catalogue"}); err == nil {
		t.Error("an unknown model was accepted")
	}
}

// A cancelled take leaves a load nobody will await. Left in place it would block
// every later one, so the next model would never reach the engine.
func TestCancelReleasesThePendingLoad(t *testing.T) {
	service := NewService()

	service.mu.Lock()
	service.beginLoad(newStubLoader(0, nil), "/models/first.gguf")
	service.mu.Unlock()

	service.Cancel()

	service.mu.Lock()
	pending := service.pendingLoad
	service.mu.Unlock()
	if pending != nil {
		t.Fatal("cancel left a load pending")
	}

	second := newStubLoader(0, nil)
	service.mu.Lock()
	service.beginLoad(second, "/models/second.gguf")
	service.mu.Unlock()

	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("the next load never ran")
	}
	if err := service.awaitLoad(); err != nil {
		t.Fatal(err)
	}
	if service.loaded != "/models/second.gguf" {
		t.Errorf("loaded = %q, want the second model", service.loaded)
	}
}

// A take that never starts must not strand its load either.
func TestDropPendingLoadClearsIt(t *testing.T) {
	service := NewService()

	service.mu.Lock()
	service.beginLoad(newStubLoader(0, nil), "/models/whisper.gguf")
	service.dropPendingLoad()
	pending := service.pendingLoad
	service.mu.Unlock()

	if pending != nil {
		t.Error("the load was left pending")
	}
}

// A load that was superseded must not report itself resident when it lands late.
func TestSupersededLoadDoesNotClaimResidency(t *testing.T) {
	service := NewService()
	slow := newStubLoader(150*time.Millisecond, nil)

	service.mu.Lock()
	service.beginLoad(slow, "/models/superseded.gguf")
	service.beginLoad(newStubLoader(0, nil), "/models/current.gguf")
	service.mu.Unlock()

	if err := service.awaitLoad(); err != nil {
		t.Fatal(err)
	}

	// Outlive the superseded load, which finishes well after the current one.
	time.Sleep(250 * time.Millisecond)

	service.mu.Lock()
	defer service.mu.Unlock()
	if service.loaded != "/models/current.gguf" {
		t.Errorf("loaded = %q, want the model that actually replaced it", service.loaded)
	}
}
