package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/config"
)

// The connection test builds the provider from what is on screen, for every kind there is.
func TestTheConnectionTestKnowsEveryProvider(t *testing.T) {
	test.NewApp()
	cfg := config.Default()
	if err := cfg.AddCustomProvider("local", config.ProviderSettings{BaseURL: "http://localhost:11434/v1", NoAPIKey: true}); err != nil {
		t.Fatal(err)
	}
	w := &MainWindow{config: cfg}

	for _, name := range append(config.BuiltInProviders(), "local") {
		settings := cfg.GetProviderSettings(name)
		provider, err := w.providerUnderTest(name, settings, "key", settings.BaseURL, "model")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if provider.Name() != name {
			t.Fatalf("%s built a provider called %q", name, provider.Name())
		}
	}

	if _, err := w.providerUnderTest("nobody", config.ProviderSettings{}, "key", "", "model"); err == nil {
		t.Fatal("an unknown provider was built")
	}
}

func connectionTo(t *testing.T, handler http.HandlerFunc) ai.Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := ai.FromSettings("local", config.ProviderSettings{BaseURL: server.URL + "/v1"}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// A reasoning model can think for a minute before its first word; the test must not wait for it.
func TestTheConnectionTestNeedsOnlyTheReplyToBegin(t *testing.T) {
	provider := connectionTo(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"","reasoning":"Thinking"}}]}` + "\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})

	started := time.Now()
	if err := answers(provider, 5*time.Second); err != nil {
		t.Fatalf("a model that began thinking failed the test: %v", err)
	}
	if waited := time.Since(started); waited > time.Second {
		t.Fatalf("waited %s for a reply that had already begun", waited)
	}
}

func TestTheConnectionTestReportsWhatTheServerRefused(t *testing.T) {
	provider := connectionTo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"message":"model \"qwen9\" not found"}}`))
	})
	if err := answers(provider, 5*time.Second); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

func TestTheConnectionTestGivesUpOnASilentServer(t *testing.T) {
	provider := connectionTo(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	if err := answers(provider, 200*time.Millisecond); err == nil || !strings.Contains(err.Error(), "no answer within 200ms") {
		t.Fatalf("got %v", err)
	}
}

func TestTheConnectionTestCatchesAPageInsteadOfTheAPI(t *testing.T) {
	provider := connectionTo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html><body>Welcome</body></html>"))
	})
	if err := answers(provider, 5*time.Second); err == nil || !strings.Contains(err.Error(), "check the base URL") {
		t.Fatalf("got %v", err)
	}
}
