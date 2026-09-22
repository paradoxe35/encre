package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
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
		if provider.GetName() != name {
			t.Fatalf("%s built a provider called %q", name, provider.GetName())
		}
	}

	if _, err := w.providerUnderTest("nobody", config.ProviderSettings{}, "key", "", "model"); err == nil {
		t.Fatal("an unknown provider was built")
	}
}
