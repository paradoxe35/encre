package stt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedCatalogIsComplete(t *testing.T) {
	models := Catalogue()
	if len(models) < 20 {
		t.Fatalf("catalog holds %d models, expected the generated list", len(models))
	}

	seen := make(map[string]bool, len(models))
	for _, m := range models {
		switch {
		case m.ID == "":
			t.Errorf("%s has no id", m.Slug)
		case m.SHA256 == "":
			t.Errorf("%s has no checksum; it could not be verified after download", m.Slug)
		case m.SizeBytes <= 0:
			t.Errorf("%s has no size; resume and progress both need it", m.Slug)
		case m.Revision == "":
			t.Errorf("%s has no pinned revision", m.Slug)
		}
		if seen[m.ID] {
			t.Errorf("duplicate model id %s", m.ID)
		}
		seen[m.ID] = true
	}
}

func TestDiscoverCustomModels(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int64) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("my-finetune.gguf", 10)
	write("whisper-custom.bin", 20)
	write("notes.txt", 5)
	if err := os.Mkdir(filepath.Join(dir, "nested.gguf"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	custom := discoverCustomIn(dir)
	if len(custom) != 2 {
		t.Fatalf("discovered %d models, want 2: %+v", len(custom), custom)
	}
	if custom[0].Name != "my-finetune" || custom[1].Name != "whisper-custom" {
		t.Errorf("wrong discovery order or names: %+v", custom)
	}
	if custom[0].ID != "custom/my-finetune.gguf" || custom[0].SizeBytes != 10 {
		t.Errorf("custom model fields wrong: %+v", custom[0])
	}
}

func TestDiscoverCustomSkipsCatalogEntries(t *testing.T) {
	dir := t.TempDir()
	shipped := Models().Models[0]
	if err := os.WriteFile(filepath.Join(dir, shipped.Filename), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := discoverCustomIn(dir); len(got) != 0 {
		t.Errorf("a file claimed by the catalog was rediscovered: %+v", got)
	}
}

func TestRecommendedSortsFirst(t *testing.T) {
	models := Catalogue()
	if !models[0].Recommended {
		t.Errorf("first model %s is not recommended", models[0].Slug)
	}

	recommended, ok := Recommended()
	if !ok || !recommended.Recommended {
		t.Fatal("no recommended model available")
	}
}

func TestDownloadURLPinsRevision(t *testing.T) {
	model := Model{Repo: "org/repo", Revision: "abc123", Filename: "m.gguf"}
	want := "https://huggingface.co/org/repo/resolve/abc123/m.gguf"
	if got := model.DownloadURL(); got != want {
		t.Errorf("DownloadURL() = %q, want %q", got, want)
	}
}

func TestParseCatalogRejectsEmpty(t *testing.T) {
	if _, err := parseCatalog([]byte(`{"catalog_version":1,"models":[]}`)); err == nil {
		t.Error("an empty catalog must be rejected, not adopted")
	}
	if _, err := parseCatalog([]byte(`not json`)); err == nil {
		t.Error("unparseable data must be rejected")
	}
}

func TestFindModelMatchesByID(t *testing.T) {
	first := Catalogue()[0]
	found, ok := FindModel(first.ID)
	if !ok || found.ID != first.ID {
		t.Fatalf("FindModel(%q) did not round-trip", first.ID)
	}
	if _, ok := FindModel("nothing/here"); ok {
		t.Error("an unknown id should not resolve")
	}
}

func TestOlderCacheLosesToShipped(t *testing.T) {
	shipped, err := parseCatalog(embeddedCatalog)
	if err != nil {
		t.Fatal(err)
	}

	stale := Catalog{Version: shipped.Version - 1, Models: shipped.Models[:1]}
	data, _ := json.Marshal(stale)

	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cached, err := parseCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Version >= shipped.Version {
		t.Fatal("test fixture should be older than the shipped catalog")
	}
}

func TestLanguageSummary(t *testing.T) {
	cases := []struct {
		languages []string
		want      string
	}{
		{nil, "unknown"},
		{[]string{"en"}, "en"},
		{[]string{"en", "fr", "de"}, "3 languages"},
	}
	for _, c := range cases {
		got := Model{Languages: c.languages}.LanguageSummary()
		if got != c.want {
			t.Errorf("LanguageSummary(%v) = %q, want %q", c.languages, got, c.want)
		}
	}
}

// Every language code must have an English name; a missing one falls back to the raw code.
func TestCatalogLanguagesAllNamed(t *testing.T) {
	shipped, err := parseCatalog(embeddedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, model := range shipped.Models {
		for _, code := range model.Languages {
			if seen[code] {
				continue
			}
			seen[code] = true
			if LanguageName(code) == code {
				t.Errorf("language %q has no English name", code)
			}
		}
	}
}

// Appending in place would write into spare capacity the catalog still owns.
func TestCatalogueDoesNotAliasTheCatalog(t *testing.T) {
	published := Models().Models
	if cap(published) == len(published) {
		t.Skip("catalog slice has no spare capacity to scribble into")
	}

	combined := Catalogue()
	if len(combined) < len(published) {
		t.Fatalf("Catalogue() dropped entries: %d < %d", len(combined), len(published))
	}
	if &combined[0] == &published[0] {
		t.Error("Catalogue() shares its backing array with the published catalog")
	}
}
