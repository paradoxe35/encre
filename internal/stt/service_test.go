package stt

import (
	"context"
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

// Command order is the service's contract with Rust.
type fakeEngine struct {
	mu       sync.Mutex
	log      []string
	stopErr  error
	startErr error
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
func (f *fakeEngine) Start() error { f.record("start"); return f.startErr }
func (f *fakeEngine) Stop() (string, error) {
	f.mu.Lock()
	f.log = append(f.log, "stop")
	gate, err := f.stopGate, f.stopErr
	f.mu.Unlock()

	if gate != nil {
		<-gate
	}
	return "text", err
}

func (f *fakeEngine) gateStop(gate chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopGate = gate
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
	gate := make(chan struct{})
	fake.gateStop(gate)

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
	close(gate)
	fake.gateStop(nil)
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

func TestCloseDoesNotWaitForATakeStillTranscribing(t *testing.T) {
	service, fake, cfg := newTestService(t, true)
	gate := make(chan struct{})
	fake.gateStop(gate)
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		service.StopRecording()
	}()
	for !slices.Contains(fake.entries(), "stop") {
		time.Sleep(5 * time.Millisecond)
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		service.Close()
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for the transcription")
	}
	if slices.Contains(fake.entries(), "close") {
		t.Fatal("the engine was closed under a take still transcribing")
	}

	close(gate)
	<-stopped
}

func TestAFailedStartReleasesTheModelWhenNotKept(t *testing.T) {
	service, fake, cfg := newTestService(t, false)
	fake.startErr = errors.New("no microphone")

	if err := service.StartRecording(cfg); err == nil {
		t.Fatal("expected the start failure to surface")
	}
	if got := commands(fake.entries(), "use", "start", "unload"); len(got) != 3 || got[2] != "unload" {
		t.Errorf("commands = %v, want the model unloaded after the failed start", got)
	}

	fake.startErr = nil
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	if got := commands(fake.entries(), "use"); len(got) != 2 {
		t.Errorf("use commands = %v, want the model asked for again", got)
	}
}

func TestAFailedStartKeepsAModelThatIsKept(t *testing.T) {
	service, fake, cfg := newTestService(t, true)
	fake.startErr = errors.New("no microphone")

	if err := service.StartRecording(cfg); err == nil {
		t.Fatal("expected the start failure to surface")
	}
	if slices.Contains(fake.entries(), "unload") {
		t.Error("a model kept in memory was unloaded")
	}
}

func TestWitAIPartialTranscriptIsTyped(t *testing.T) {
	fake := &fakeEngine{}
	service := &Service{store: &Store{dir: t.TempDir()}, newEngine: func() (speechEngine, error) { return fake, nil }}
	service.activeEngine = config.SpeechWitAI
	service.speech, service.recording, service.takes = fake, true, 1

	previous := witaiTranscribe
	witaiTranscribe = func(context.Context, []byte, string) (string, error) {
		return "the first minute", errors.New("wit.ai chunk 4 of 9: timeout")
	}
	t.Cleanup(func() { witaiTranscribe = previous })

	text, err := service.StopRecording()
	if !errors.Is(err, ErrPartial) || text != "the first minute" {
		t.Fatalf("got %q, %v, want the partial transcript and ErrPartial", text, err)
	}
}

func TestClosingStopsAHostedTakeStillTranscribing(t *testing.T) {
	fake := &fakeEngine{}
	service := &Service{store: &Store{dir: t.TempDir()}, newEngine: func() (speechEngine, error) { return fake, nil }}
	service.activeEngine = config.SpeechWitAI
	service.speech, service.recording, service.takes = fake, true, 1

	previous := witaiTranscribe
	started := make(chan struct{})
	witaiTranscribe = func(ctx context.Context, _ []byte, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	t.Cleanup(func() { witaiTranscribe = previous })

	stopped := make(chan error, 1)
	go func() {
		_, err := service.StopRecording()
		stopped <- err
	}()
	<-started
	service.Close()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want the take cancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing did not stop the take")
	}
}

func TestWitAIFailureWithNoTextStillFails(t *testing.T) {
	fake := &fakeEngine{}
	service := &Service{store: &Store{dir: t.TempDir()}, newEngine: func() (speechEngine, error) { return fake, nil }}
	service.activeEngine = config.SpeechWitAI
	service.speech, service.recording, service.takes = fake, true, 1

	previous := witaiTranscribe
	witaiTranscribe = func(context.Context, []byte, string) (string, error) { return "", errors.New("offline") }
	t.Cleanup(func() { witaiTranscribe = previous })

	if _, err := service.StopRecording(); err == nil {
		t.Fatal("expected the failure to surface")
	}
}

func TestAFailedTakeStillUnloadsAModelNotKept(t *testing.T) {
	service, fake, cfg := newTestService(t, false)
	fake.stopErr = errors.New("microphone unavailable")

	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StopRecording(); err == nil {
		t.Fatal("test setup: the stop did not fail")
	}
	if got := commands(fake.entries(), "unload"); len(got) != 1 {
		t.Fatalf("a failed take left the model loaded: %v", fake.entries())
	}

	fake.stopErr = nil
	take(t, service, cfg)
	if got := commands(fake.entries(), "use"); len(got) != 2 {
		t.Errorf("the next take did not ask for the model again: %v", fake.entries())
	}
}

func TestATakeThatOutlivesCloseDoesNotCountAgainstTheNextEngine(t *testing.T) {
	service, _, cfg := newTestService(t, false)
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	old := service.speech
	service.Close()
	service.finishTake(old)
	if service.takes != 0 {
		t.Fatalf("takes = %d after a closed engine's take ended", service.takes)
	}
}

func TestAnEngineLeftToATakeIsClosedWhenThatTakeEnds(t *testing.T) {
	service, fake, cfg := newTestService(t, false)
	gate := make(chan struct{})
	fake.gateStop(gate)
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		service.StopRecording()
		close(stopped)
	}()
	for !slices.Contains(fake.entries(), "stop") {
		time.Sleep(time.Millisecond)
	}

	service.Close()
	if slices.Contains(fake.entries(), "close") {
		t.Fatal("closed the engine under the take still transcribing")
	}
	close(gate)
	<-stopped
	if got := commands(fake.entries(), "close"); len(got) != 1 {
		t.Fatalf("the engine left to the take was not closed once it ended: %v", fake.entries())
	}
}

func TestClosingWhileRecordingStopsTheMicrophone(t *testing.T) {
	service, fake, cfg := newTestService(t, false)
	if err := service.StartRecording(cfg); err != nil {
		t.Fatal(err)
	}
	service.Close()
	if !slices.Contains(fake.entries(), "cancel") || !slices.Contains(fake.entries(), "close") {
		t.Fatalf("an open recording outlived Close: %v", fake.entries())
	}
}

func TestEveryEngineLeftToATakeIsClosedThoughCloseCameTwice(t *testing.T) {
	service, _, cfg := newTestService(t, false)
	var engines []*fakeEngine
	gate := make(chan struct{})
	service.newEngine = func() (speechEngine, error) {
		engine := &fakeEngine{}
		engine.gateStop(gate)
		engines = append(engines, engine)
		return engine, nil
	}

	var stopped sync.WaitGroup
	for range 2 {
		if err := service.StartRecording(cfg); err != nil {
			t.Fatal(err)
		}
		engine := engines[len(engines)-1]
		stopped.Add(1)
		go func() {
			defer stopped.Done()
			service.StopRecording()
		}()
		for !slices.Contains(engine.entries(), "stop") {
			time.Sleep(time.Millisecond)
		}
		service.Close()
	}
	close(gate)
	stopped.Wait()

	for i, engine := range engines {
		if got := commands(engine.entries(), "close"); len(got) != 1 {
			t.Errorf("engine %d left to its take was closed %d times: %v", i, len(got), engine.entries())
		}
	}
}

func TestSwitchingToAHostedEngineFreesAKeptModel(t *testing.T) {
	service, fake, cfg := newTestService(t, true)
	if err := service.Prepare(cfg); err != nil {
		t.Fatal(err)
	}

	cfg.Engine = config.SpeechRemote
	if err := service.Prepare(cfg); err != nil {
		t.Fatal(err)
	}
	if got := commands(fake.entries(), "unload"); len(got) != 1 {
		t.Errorf("unload commands = %v, want the kept model freed once the hosted engine is chosen", got)
	}

	cfg.Engine = config.SpeechLocal
	if err := service.Prepare(cfg); err != nil {
		t.Fatal(err)
	}
	if got := commands(fake.entries(), "use"); len(got) != 2 {
		t.Errorf("use commands = %v, want the model loaded again on switching back", got)
	}
}
