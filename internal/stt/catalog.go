package stt

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/utils"
)

// Shipped so the list renders offline and on first run; regenerate with
// `go run ./cmd/gen-catalog`.
//
//go:embed models.json
var embeddedCatalog []byte

const (
	catalogMaxAge = 24 * time.Hour
	catalogMaxLen = 8 << 20

	// RefreshTimeout bounds one whole rebuild: ~150 hub requests at six in
	// flight normally finish in well under a minute.
	RefreshTimeout = 3 * time.Minute
	// How often the scheduler re-checks the cache's age while the app runs.
	refreshCheckInterval = 3 * time.Hour
)

type Catalog struct {
	Version int     `json:"catalog_version"`
	Source  string  `json:"source"`
	Models  []Model `json:"models"`

	origin  string
	fetched time.Time
}

var (
	catalogMu sync.RWMutex
	active    *Catalog
)

// A variable so tests can cache into a scratch directory.
var cachePath = func() string { return utils.AppHomeDir("catalog.json") }

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

var refreshMu sync.Mutex

// Refresh rebuilds the list from Hugging Face regardless of the cache's age.
// The result goes through the same parser as the shipped file before it is
// written, so a broken build never displaces a working list. Calls are
// serialised: the scheduler and the settings button may overlap.
func Refresh(ctx context.Context) error {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	catalog, err := FetchCatalog(ctx)
	if err != nil {
		return err
	}
	data, err := EncodeCatalog(catalog)
	if err != nil {
		return err
	}
	return adopt(data)
}

// adopt makes a fetched list current, caches it for the next launch and
// tells the listeners.
func adopt(data []byte) error {
	fetched, err := parseCatalog(data)
	if err != nil {
		return fmt.Errorf("fetched catalog is unusable: %w", err)
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
	notifyCatalogChanged()
	return nil
}

var (
	listenersMu  sync.Mutex
	listeners    = map[int]func(){}
	nextListener int
)

// OnCatalogChanged registers fn to run after every successful refresh, on the
// goroutine that refreshed. UI callers hand the work to their own thread.
func OnCatalogChanged(fn func()) (unsubscribe func()) {
	listenersMu.Lock()
	id := nextListener
	nextListener++
	listeners[id] = fn
	listenersMu.Unlock()

	return func() {
		listenersMu.Lock()
		delete(listeners, id)
		listenersMu.Unlock()
	}
}

// Snapshotted so a listener that unsubscribes while being called does not
// deadlock on the map.
func notifyCatalogChanged() {
	listenersMu.Lock()
	current := make([]func(), 0, len(listeners))
	for _, fn := range listeners {
		current = append(current, fn)
	}
	listenersMu.Unlock()

	for _, fn := range current {
		fn()
	}
}

var (
	schedulerMu   sync.Mutex
	schedulerStop chan struct{}
	schedulerDone chan struct{}
)

// StartRefreshing refreshes at launch when the cache is stale and keeps
// checking while the app runs, so a machine left open for days still learns
// about new models. Never blocks startup; failures are not surfaced since the
// current list still works.
func StartRefreshing() {
	startRefreshing(refreshCheckInterval)
}

func startRefreshing(every time.Duration) {
	schedulerMu.Lock()
	defer schedulerMu.Unlock()
	if schedulerStop != nil {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	schedulerStop, schedulerDone = stop, done

	go func() {
		defer close(done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			refreshIfStale(stop)
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

// StopRefreshing halts the scheduler and waits for any refresh it started to
// abort, so shutdown does not race a cache write.
func StopRefreshing() {
	schedulerMu.Lock()
	stop, done := schedulerStop, schedulerDone
	schedulerStop, schedulerDone = nil, nil
	schedulerMu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

func refreshIfStale(stop <-chan struct{}) {
	if !stale() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), RefreshTimeout)
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := Refresh(ctx); err != nil {
		logger.Info("Keeping the current model catalog", "reason", err)
	}
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
