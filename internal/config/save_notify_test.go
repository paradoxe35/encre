package config

import (
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/utils"
)

// resetListeners clears the package listeners for one test and puts them back.
func resetListeners(t *testing.T) {
	t.Helper()

	listenerMutex.Lock()
	previous := listeners
	listeners = nil
	listenerMutex.Unlock()

	t.Cleanup(func() {
		listenerMutex.Lock()
		listeners = previous
		listenerMutex.Unlock()
	})
}

// SetAPIKey used to save, publishing a config the rest of saveSettings had not
// been written into: a hotkey enabled in the same save went unregistered.
func TestSetAPIKeyDoesNotPublish(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	resetListeners(t)

	// Listeners run on their own goroutine, so wait one out rather than count.
	published := make(chan struct{}, 4)
	RegisterListener(func(*Config) {
		select {
		case published <- struct{}{}:
		default:
		}
	})

	cfg := Default()
	if err := cfg.SetAPIKey(BuiltInOpenAI, "sk-test"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-published:
		t.Error("SetAPIKey published; the save that follows it should be the only one")
	case <-time.After(200 * time.Millisecond):
	}
}

// The key still has to be there for the save that follows.
func TestSetAPIKeyStoresTheKey(t *testing.T) {
	cfg := Default()
	if err := cfg.SetAPIKey(BuiltInOpenAI, "sk-test"); err != nil {
		t.Fatal(err)
	}

	got, err := cfg.GetAPIKey(BuiltInOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-test" {
		t.Errorf("key = %q, want sk-test", got)
	}
}
