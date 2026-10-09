package stt

import (
	"os"
	"strings"
	"testing"
)

// The runnable list is only right for the library version it was taken from.
func TestRunnableArchitecturesMatchTheBundledLibrary(t *testing.T) {
	manifest, err := os.ReadFile("../../rust-ffi/Cargo.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `transcribe-cpp = { version = "`+transcribeCPPVersion+`"`) {
		t.Fatalf("rust-ffi no longer pins transcribe-cpp %s: review the runnable architectures for the new version, then update transcribeCPPVersion", transcribeCPPVersion)
	}
}

func TestTheShippedCatalogueListsOnlyRunnableModels(t *testing.T) {
	shipped, err := parseCatalog(embeddedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range shipped.Models {
		if strings.Contains(model.Slug, "sortformer") {
			t.Errorf("the shipped catalogue still lists %s, which the library cannot transcribe with", model.Slug)
		}
	}
}
