package stt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/config"
)

// fakeEngine records every command in order, which is what the service's
// contract with Rust is about.
type fakeEngine struct {
	mu       sync.Mutex
	log      []string
	stopErr  error
	stopGate chan struct{}
}

func (f *fakeEngine) record(entry string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, entry)
}

func (f *fakeEngine) entries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

func (f *fakeEngine) UseModel(path string) error { f.record("use " + filepath.Base(path)); return nil }
func (f *fakeEngine) Unload()                    { f.record("unload") }
func (f *fakeEngine) SetDevice(name string) error {
	f.record("device " + name)
	return nil
}
func (f *fakeEngine) SetLanguage(code string) error { f.record("language " + code); return nil }
func (f *fakeEngine) SetCaptureOnly(enabled bool) error {
	f.record(fmt.Sprintf("capture %v", enabled))
	return nil
}
func (f *fakeEngine) Start() error { f.record("start"); return nil }
func (f *fakeEngine) Stop() (string, error) {
	f.record("stop")
	if f.stopGate != nil {
		<-f.stopGate
	}
	return "text", f.stopErr
}
func (f *fakeEngine) StopPCM() ([]byte, error) { f.record("stop-pcm"); return nil, nil }
func (f *fakeEngine) Cancel()                  { f.record("cancel") }
func (f *fakeEngine) Close()                   { f.record("close") }

func downloadedModel(t *testing.T) (*Store, Model) {
	t.Helper()
	store := &Store{dir: t.TempDir()}
	model := Catalogue()[0]

	file, err := os.Create(store.Path(model))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(model.SizeBytes); err != nil {
		t.Fatal(err)
	}
	file.Close()
	return store, model
}

func newTestService(t *testing.T, keep bool) (*Service, *fakeEngine, config.SpeechConfig) {
	t.Helper()
	store, model := downloadedModel(t)
	fake := &fakeEngine{}
	service := &Service{store: store, newEngine: func() (speechEngine, error) { return fake, nil }}
	cfg := config.SpeechConfig{Engine: config.SpeechLocal, ModelID: model.ID, KeepModelLoaded: keep}
	return service, fake, cfg
}

func take(t *testing.T, service *Service, cfg config.SpeechConfig) {
	t.Helper()
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StopRecording(); err != nil {
		t.Fatal(err)
	}
}

func commands(entries []string, prefixes ...string) []string {
	var out []string
	for _, entry := range entries {
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				out = append(out, entry)
			}
		}
	}
	return out
}

func TestModelIsAskedForOnceWhileKept(t *testing.T) {
	service, fake, cfg := newTestService(t, true)

	take(t, service, cfg)
	take(t, service, cfg)

	got := commands(fake.entries(), "use", "start", "unload")
	want := []string{"use " + filepath.Base(service.store.Path(Catalogue()[0])), "start", "start"}
	if !slices.Equal(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}
}

func TestModelIsReleasedAndReaskedWhenNotKept(t *testing.T) {
	service, fake, cfg := newTestService(t, false)

	take(t, service, cfg)
	take(t, service, cfg)

	got := commands(fake.entries(), "use", "start", "stop", "unload")
	if len(got) != 8 || got[3] != "unload" || !strings.HasPrefix(got[4], "use") || got[7] != "unload" {
		t.Errorf("commands = %v, want use/start/stop/unload twice, the second use after the unload", got)
	}
}

func TestUnloadWaitsForTheTakeStillInFlight(t *testing.T) {
	service, fake, cfg := newTestService(t, false)
	fake.stopGate = make(chan struct{})

	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.StopRecording()
		firstDone <- err
	}()
	for len(commands(fake.entries(), "stop")) == 0 {
		time.Sleep(time.Millisecond)
	}

	// The second take begins while the first is still transcribing.
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	close(fake.stopGate)
	fake.stopGate = nil
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	if slices.Contains(fake.entries(), "unload") {
		t.Fatal("unloaded under the take still in flight")
	}
	if _, err := service.StopRecording(); err != nil {
		t.Fatal(err)
	}
	if got := commands(fake.entries(), "unload"); len(got) != 1 {
		t.Errorf("unload commands = %d, want exactly one once the last take is out", len(got))
	}
}

func TestStopErrorAsksForTheModelAgain(t *testing.T) {
	service, fake, cfg := newTestService(t, true)
	fake.stopErr = errors.New("failed to load model")

	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StopRecording(); err == nil {
		t.Fatal("expected the load failure to surface")
	}
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}

	if got := commands(fake.entries(), "use"); len(got) != 2 {
		t.Errorf("use commands = %v, want the model asked for again after the failure", got)
	}
}

func TestCancelReleasesTheModelWhenNotKept(t *testing.T) {
	service, fake, cfg := newTestService(t, false)

	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	service.Cancel()

	got := commands(fake.entries(), "cancel", "unload")
	if !slices.Equal(got, []string{"cancel", "unload"}) {
		t.Errorf("commands = %v, want cancel then unload", got)
	}
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	if got := commands(fake.entries(), "use"); len(got) != 2 {
		t.Errorf("use commands = %v, want the model asked for again after the unload", got)
	}
}

func TestCancelWithoutATakeChangesNothing(t *testing.T) {
	service, fake, cfg := newTestService(t, false)
	if err := service.Prepare(cfg); err != nil {
		t.Fatal(err)
	}

	service.Cancel()

	if slices.Contains(fake.entries(), "unload") {
		t.Error("cancel with no take in flight unloaded the model")
	}
	if service.takes != 0 {
		t.Errorf("takes = %d, want 0", service.takes)
	}
}

func TestModelPathRefusesWhatCannotBeLoaded(t *testing.T) {
	service := &Service{store: &Store{dir: t.TempDir()}}

	if _, err := service.modelPath(config.SpeechConfig{}); !errors.Is(err, ErrNoModel) {
		t.Errorf("no model selected = %v, want ErrNoModel", err)
	}
	if _, err := service.modelPath(config.SpeechConfig{ModelID: "not-in-the-catalogue"}); err == nil {
		t.Error("an unknown model was accepted")
	}
	if _, err := service.modelPath(config.SpeechConfig{ModelID: Catalogue()[0].ID}); err == nil {
		t.Error("a model that is not downloaded was accepted")
	}
}

func TestPrepareLoadsOnlyWhenTheModelIsKept(t *testing.T) {
	service, fake, cfg := newTestService(t, false)
	if err := service.Prepare(cfg); err != nil {
		t.Fatal(err)
	}
	if got := commands(fake.entries(), "use"); len(got) != 0 {
		t.Errorf("preloaded %v although the model is not kept in memory", got)
	}

	cfg.KeepModelLoaded = true
	if err := service.Prepare(cfg); err != nil {
		t.Fatal(err)
	}
	if got := commands(fake.entries(), "use"); len(got) != 1 {
		t.Errorf("use commands = %v, want the model preloaded when kept", got)
	}
}
