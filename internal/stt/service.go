package stt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	locale "github.com/jeandeaual/go-locale"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/input"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/stt/witai"
)

var ErrNoModel = errors.New("no speech model selected - choose one in Settings")

// Service turns hotkey edges into transcripts. The engine is created lazily so
// an app that never dictates never opens an audio device.
type Service struct {
	store *Store

	mu           sync.Mutex
	speech       *input.FFISpeech
	recording    bool
	loaded       string
	device       string
	language     string
	captureOnly  bool
	activeEngine config.SpeechEngine
	witaiLang    string
	remoteCfg    config.SpeechConfig
	keepLoaded   bool
	pendingLoad  chan error
	loadingPath  string
	loadEpoch    uint64
}

func NewService() *Service {
	return &Service{store: NewStore()}
}

func (s *Service) Store() *Store { return s.store }

// OnLevel receives microphone level while recording, from a background thread.
func (s *Service) OnLevel(handler func(float32)) { input.OnLevel(handler) }

func (s *Service) engine() (*input.FFISpeech, error) {
	if s.speech != nil {
		return s.speech, nil
	}

	speech, err := input.NewFFISpeech()
	if err != nil {
		return nil, err
	}

	s.speech = speech
	s.loaded = ""
	s.device = ""
	s.language = ""
	s.captureOnly = false
	return speech, nil
}

// Prepare loads the model and opens the microphone ahead of the first dictation; idempotent.
func (s *Service) Prepare(cfg config.SpeechConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	speech, err := s.engine()
	if err != nil {
		return err
	}
	if err := s.applyEngine(speech, cfg); err != nil {
		return err
	}

	s.applyDevice(speech, cfg)
	return nil
}

// applyEngine sets up the selected transcription backend: a resident local model, or
// capture-only audio for witai/remote, which transcribe from the raw take.
func (s *Service) applyEngine(speech *input.FFISpeech, cfg config.SpeechConfig) error {
	// A build with no embedded keys hides Wit.ai in the UI, but a config carried
	// over from a build that had them can still name it; fall back to local.
	engine := cfg.Engine
	if engine == config.SpeechWitAI && !witai.Available() {
		engine = config.SpeechLocal
	}
	s.activeEngine = engine

	switch engine {
	case config.SpeechWitAI:
		if err := s.applyCaptureOnly(speech, true); err != nil {
			return err
		}
		s.witaiLang = cfg.Language
		return nil
	case config.SpeechRemote:
		if err := s.applyCaptureOnly(speech, true); err != nil {
			return err
		}
		s.remoteCfg = cfg
		return nil
	}

	s.keepLoaded = cfg.KeepModelLoaded

	path, err := s.modelPath(cfg)
	if err != nil {
		return err
	}
	s.applyLanguage(speech, cfg)

	// Resident already: the take can stream, which transcribes as it is spoken.
	if s.loaded == path {
		return s.applyCaptureOnly(speech, false)
	}

	// Not resident: capture rather than wait for the load, or the first words go
	// missing while the model comes up. The load runs alongside and is waited on
	// at stop, when it has usually finished.
	if err := s.applyCaptureOnly(speech, true); err != nil {
		return err
	}
	s.beginLoad(speech, path)
	return nil
}

// modelLoader is the part of the engine beginLoad needs, so the concurrency
// around it can be exercised without an audio device.
type modelLoader interface {
	Load(path string) error
}

// beginLoad loads the model on its own goroutine. Rust runs the engine on a
// thread of its own, so this does not hold up the capture that follows.
func (s *Service) beginLoad(speech modelLoader, path string) {
	if s.pendingLoad != nil && s.loadingPath == path {
		return
	}
	s.dropPendingLoad()

	done := make(chan error, 1)
	s.pendingLoad = done
	s.loadingPath = path
	s.loadEpoch++
	epoch := s.loadEpoch

	go func() {
		err := speech.Load(path)

		s.mu.Lock()
		// Only the newest load says what is resident: a superseded one finishing
		// late would otherwise name a model the engine has already replaced.
		if s.loadEpoch == epoch {
			if err == nil {
				s.loaded = path
			} else {
				s.loaded = ""
			}
		}
		s.mu.Unlock()

		done <- err
	}()
}

// dropPendingLoad releases a load no take is waiting for any more; the caller
// holds the lock. The load itself runs on, and reports what it loaded unless a
// newer one has since superseded it.
func (s *Service) dropPendingLoad() {
	pending := s.pendingLoad
	s.pendingLoad = nil
	s.loadingPath = ""

	if pending != nil {
		go func() { <-pending }()
	}
}

// awaitLoad blocks until a load started for this take has finished.
func (s *Service) awaitLoad() error {
	s.mu.Lock()
	pending := s.pendingLoad
	s.pendingLoad = nil
	s.loadingPath = ""
	s.mu.Unlock()

	if pending == nil {
		return nil
	}
	return <-pending
}

// applyCaptureOnly is a no-op when nothing changed, matching applyDevice/applyLanguage.
func (s *Service) applyCaptureOnly(speech *input.FFISpeech, capture bool) error {
	if s.captureOnly == capture {
		return nil
	}
	if err := speech.SetCaptureOnly(capture); err != nil {
		return err
	}
	s.captureOnly = capture
	return nil
}

// applyDevice is a no-op when nothing changed, so a warmed stream isn't torn down and reopened every dictation.
func (s *Service) applyDevice(speech *input.FFISpeech, cfg config.SpeechConfig) {
	if s.device == cfg.InputDevice {
		return
	}
	if err := speech.SetDevice(cfg.InputDevice); err != nil {
		logger.Warn("Could not select the microphone", "device", cfg.InputDevice, "error", err)
		return
	}
	s.device = cfg.InputDevice
}

// applyLanguage tells the engine what to listen for; see TranscribeLanguage.
func (s *Service) applyLanguage(speech *input.FFISpeech, cfg config.SpeechConfig) {
	model, ok := FindModel(cfg.ModelID)
	if !ok {
		return
	}

	code := model.TranscribeLanguage(cfg.Language, SystemLanguage())
	if s.language == code {
		return
	}
	if err := speech.SetLanguage(code); err != nil {
		logger.Warn("Could not set the speech language", "language", code, "error", err)
		return
	}
	s.language = code
}

// modelPath resolves the file the take will be transcribed with, and refuses
// early on the cases a load could not recover from anyway.
func (s *Service) modelPath(cfg config.SpeechConfig) (string, error) {
	if cfg.ModelID == "" {
		return "", ErrNoModel
	}

	model, ok := FindModel(cfg.ModelID)
	if !ok {
		return "", fmt.Errorf("unknown model %q", cfg.ModelID)
	}
	if !s.store.Downloaded(model) {
		return "", fmt.Errorf("%s is not downloaded yet", model.Name)
	}

	return s.store.Path(model), nil
}

// StartRecording loads the model too, so the first dictation needs no setup.
func (s *Service) StartRecording(cfg config.SpeechConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.recording {
		return errors.New("already recording")
	}

	speech, err := s.engine()
	if err != nil {
		return err
	}
	if err := s.applyEngine(speech, cfg); err != nil {
		return err
	}

	s.applyDevice(speech, cfg)
	if err := speech.Start(); err != nil {
		s.dropPendingLoad()
		return err
	}

	s.recording = true
	return nil
}

// StopRecording blocks for as long as transcription takes.
func (s *Service) StopRecording() (string, error) {
	s.mu.Lock()
	speech, recording, activeEngine, lang, remoteCfg := s.speech, s.recording, s.activeEngine, s.witaiLang, s.remoteCfg
	s.recording = false
	s.mu.Unlock()

	if speech == nil || !recording {
		return "", errors.New("not recording")
	}

	switch activeEngine {
	case config.SpeechRemote:
		return s.stopRemote(speech, remoteCfg)
	case config.SpeechWitAI:
		return s.stopWitAI(speech, lang)
	}

	// Stop before awaiting the load: the batch pass queues behind the load anyway,
	// and waiting first would keep the microphone open until the load finished.
	text, err := speech.Stop()

	if loadErr := s.awaitLoad(); loadErr != nil {
		return "", loadErr
	}

	s.unloadIfNotKept(speech)
	if err != nil {
		return "", err
	}

	logger.Info("Dictation transcribed", "characters", len(text))
	return text, nil
}

func (s *Service) stopWitAI(speech *input.FFISpeech, lang string) (string, error) {
	pcm, err := speech.StopPCM()
	if err != nil {
		return "", err
	}

	// Generous: a long dictation is sent to Wit.ai in several sequential chunk requests.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	text, err := witai.Transcribe(ctx, pcm, lang)
	if err != nil {
		return "", err
	}

	logger.Info("Dictation transcribed", "characters", len(text), "engine", "witai")
	return text, nil
}

func (s *Service) stopRemote(speech *input.FFISpeech, cfg config.SpeechConfig) (string, error) {
	pcm, err := speech.StopPCM()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), remoteRequestTimeout)
	defer cancel()

	text, err := RemoteTranscribe(ctx, cfg, pcm)
	if err != nil {
		return "", err
	}

	logger.Info("Dictation transcribed", "characters", len(text), "engine", "remote")
	return text, nil
}

type modelUnloader interface {
	Unload()
}

// A take already recording on the model keeps it; that take's own stop unloads.
func (s *Service) unloadIfNotKept(speech modelUnloader) {
	s.mu.Lock()
	if s.keepLoaded || s.loaded == "" || s.recording {
		s.mu.Unlock()
		return
	}
	s.loaded = ""
	s.mu.Unlock()

	speech.Unload()
	logger.Info("Speech model unloaded")
}

func (s *Service) Cancel() {
	s.mu.Lock()
	speech := s.speech
	s.recording = false
	s.dropPendingLoad()
	s.mu.Unlock()

	if speech != nil {
		speech.Cancel()
	}
}

// TranscribeFile verifies a model against a 16 kHz mono WAV.
func (s *Service) TranscribeFile(cfg config.SpeechConfig, path string) (string, error) {
	s.mu.Lock()
	speech, err := s.engine()
	var modelFile string
	if err == nil {
		modelFile, err = s.modelPath(cfg)
	}
	resident := s.loaded == modelFile
	s.mu.Unlock()

	if err != nil {
		return "", err
	}

	if !resident {
		if err := speech.Load(modelFile); err != nil {
			s.mu.Lock()
			s.loaded = ""
			s.mu.Unlock()
			return "", err
		}
		s.mu.Lock()
		s.loaded = modelFile
		s.mu.Unlock()
	}

	return speech.TranscribeFile(path)
}

// Devices lists microphones; may be the first call that opens an audio device, since the engine is lazy.
func (s *Service) Devices() []input.Device {
	return input.InputDevices()
}

func (s *Service) Close() {
	s.mu.Lock()
	speech := s.speech
	s.speech = nil
	s.recording = false
	s.mu.Unlock()

	if speech != nil {
		speech.Close()
	}
}

// SystemLanguage is the base code of the system locale.
func SystemLanguage() string {
	tag, err := locale.GetLocale()
	if err != nil {
		return ""
	}
	tag = strings.ReplaceAll(tag, "_", "-")
	base, _, _ := strings.Cut(tag, "-")
	return strings.ToLower(base)
}
