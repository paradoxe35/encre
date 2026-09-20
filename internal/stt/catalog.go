package stt

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/utils"
)

// Shipped so the list renders offline and on first run; regenerate with scripts/gen_catalog.py.
//
//go:embed models.json
var embeddedCatalog []byte

// Where a newer list is fetched from, so models can be added between releases.
const CatalogURL = "https://raw.githubusercontent.com/paradoxe35/encre/main/internal/stt/models.json"

const (
	catalogMaxAge = 24 * time.Hour
	catalogMaxLen = 8 << 20
)

type Catalog struct {
	Version int     `json:"catalog_version"`
	Source  string  `json:"source"`
	Models  []Model `json:"models"`

	origin  string
	fetched time.Time
}

func (c *Catalog) Origin() string     { return c.origin }
func (c *Catalog) Fetched() time.Time { return c.fetched }

var (
	catalogMu sync.RWMutex
	active    *Catalog
)

func cachePath() string { return utils.AppHomeDir("catalog.json") }

// A cached download wins over the shipped copy.
func Models() *Catalog {
	catalogMu.RLock()
	current := active
	catalogMu.RUnlock()
	if current != nil {
		return current
	}

	catalogMu.Lock()
	defer catalogMu.Unlock()
	if active == nil {
		active = loadBest()
	}
	return active
}

// Model files not claimed by the catalog: fine-tuned or community models.
func discoverCustom() []Model {
	return discoverCustomIn(utils.AppHomeDir("models"))
}

func discoverCustomIn(dir string) []Model {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	claimed := make(map[string]bool, len(Models().Models))
	for _, model := range Models().Models {
		claimed[model.Filename] = true
	}

	var custom []Model
	for _, entry := range entries {
		if entry.IsDir() || claimed[entry.Name()] {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".gguf") && !strings.HasSuffix(entry.Name(), ".bin") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		custom = append(custom, Model{
			ID:        "custom/" + entry.Name(),
			Slug:      entry.Name(),
			Name:      strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Filename:  entry.Name(),
			SizeBytes: info.Size(),
		})
	}
	sort.Slice(custom, func(i, j int) bool { return custom[i].Name < custom[j].Name })
	return custom
}

func loadBest() *Catalog {
	shipped, err := parseCatalog(embeddedCatalog)
	if err != nil {
		panic("stt: embedded catalog is invalid: " + err.Error())
	}
	shipped.origin = "embedded"

	data, err := os.ReadFile(cachePath())
	if err != nil {
		return shipped
	}

	cached, err := parseCatalog(data)
	if err != nil {
		logger.Warn("Ignoring an unreadable catalog cache", "error", err)
		return shipped
	}

	// A cache older than what shipped means the app was updated since.
	if cached.Version < shipped.Version {
		return shipped
	}

	cached.origin = "cached"
	if info, err := os.Stat(cachePath()); err == nil {
		cached.fetched = info.ModTime()
	}
	return cached
}

func parseCatalog(data []byte) (*Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}
	if len(catalog.Models) == 0 {
		return nil, errors.New("catalog contains no models")
	}

	sort.SliceStable(catalog.Models, func(i, j int) bool {
		if catalog.Models[i].Rank != catalog.Models[j].Rank {
			return catalog.Models[i].Rank < catalog.Models[j].Rank
		}
		return catalog.Models[i].AccuracyScore > catalog.Models[j].AccuracyScore
	})
	return &catalog, nil
}

func stale() bool {
	catalog := Models()
	return catalog.origin == "embedded" || time.Since(catalog.fetched) > catalogMaxAge
}

// Parsed before writing, so a truncated download never displaces a working list.
func Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CatalogURL, nil)
	if err != nil {
		return err
	}

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog fetch returned %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, catalogMaxLen))
	if err != nil {
		return err
	}

	fetched, err := parseCatalog(data)
	if err != nil {
		return fmt.Errorf("published catalog is unusable: %w", err)
	}

	if err := os.WriteFile(cachePath(), data, 0o644); err != nil {
		logger.Warn("Could not cache the model catalog", "error", err)
	}

	fetched.origin = "cached"
	fetched.fetched = time.Now()

	catalogMu.Lock()
	active = fetched
	catalogMu.Unlock()

	logger.Info("Model catalog refreshed", "models", len(fetched.Models), "version", fetched.Version)
	return nil
}

// Failures are not surfaced: the shipped list still works.
func RefreshInBackground() {
	if !stale() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := Refresh(ctx); err != nil {
			logger.Info("Keeping the shipped model catalog", "reason", err)
		}
	}()
}

// Copies rather than appends in place: the parsed slice has spare capacity, and appending
// would write into memory the catalog still owns.
func Catalogue() []Model {
	published := Models().Models
	custom := discoverCustom()

	all := make([]Model, 0, len(published)+len(custom))
	all = append(all, published...)
	return append(all, custom...)
}

func FindModel(id string) (Model, bool) {
	for _, model := range Catalogue() {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func Recommended() (Model, bool) {
	models := Models().Models
	for _, model := range models {
		if model.Recommended {
			return model, true
		}
	}
	if len(models) > 0 {
		return models[0], true
	}
	return Model{}, false
}
