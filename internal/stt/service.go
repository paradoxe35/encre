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

type speechEngine interface {
	UseModel(path string) error
	Unload()
	SetDevice(name string) error
	SetLanguage(code string) error
	SetCaptureOnly(enabled bool) error
	Start() error
	Stop() (string, error)
	StopPCM() ([]byte, error)
	Cancel()
	Close()
}

// The engine is created lazily so an app that never dictates never opens an audio device.
// Rust owns what is loaded and runs commands in order; this side only remembers what it last
// asked for and how many takes are in flight.
type Service struct {
	store     *Store
	newEngine func() (speechEngine, error)

	mu           sync.Mutex
	speech       speechEngine
	recording    bool
	takes        int
	model        string
	device       string
	language     string
	captureOnly  bool
	activeEngine config.SpeechEngine
	witaiLang    string
	remoteCfg    config.SpeechConfig
	keepLoaded   bool
}

func NewService() *Service {
	return &Service{store: NewStore(), newEngine: newFFIEngine}
}

func newFFIEngine() (speechEngine, error) {
	speech, err := input.NewFFISpeech()
	if err != nil {
		return nil, err
	}
	return speech, nil
}

func (s *Service) engine() (speechEngine, error) {
	if s.speech != nil {
		return s.speech, nil
	}

	speech, err := s.newEngine()
	if err != nil {
		return nil, err
	}

	s.speech = speech
	s.model = ""
	s.device = ""
	s.language = ""
	s.captureOnly = false
	return speech, nil
}

// Idempotent. A model not kept in memory is loaded per take instead.
func (s *Service) Prepare(cfg config.SpeechConfig) error {
	if !cfg.KeepModelLoaded {
		return nil
	}

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

// witai and remote transcribe from the raw take, so they get capture-only audio.
func (s *Service) applyEngine(speech speechEngine, cfg config.SpeechConfig) error {
	// A build without embedded keys hides Wit.ai in the UI, but a config can still name it.
	engine := cfg.Engine
	if engine == config.SpeechWitAI && !witai.Available() {
		engine = config.SpeechLocal
	}
	s.activeEngine = engine
	s.keepLoaded = cfg.KeepModelLoaded

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

	path, err := s.modelPath(cfg)
	if err != nil {
		return err
	}
	s.applyLanguage(speech, cfg)
	if err := s.applyCaptureOnly(speech, false); err != nil {
		return err
	}

	if s.model != path {
		if err := speech.UseModel(path); err != nil {
			return err
		}
		s.model = path
	}
	return nil
}

func (s *Service) applyCaptureOnly(speech speechEngine, capture bool) error {
	if s.captureOnly == capture {
		return nil
	}
	if err := speech.SetCaptureOnly(capture); err != nil {
		return err
	}
	s.captureOnly = capture
	return nil
}

func (s *Service) applyDevice(speech speechEngine, cfg config.SpeechConfig) {
	if s.device == cfg.InputDevice {
		return
	}
	if err := speech.SetDevice(cfg.InputDevice); err != nil {
		logger.Warn("Could not select the microphone", "device", cfg.InputDevice, "error", err)
		return
	}
	s.device = cfg.InputDevice
}

func (s *Service) applyLanguage(speech speechEngine, cfg config.SpeechConfig) {
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

// modelPath refuses early on the cases a load could not recover from anyway.
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
		return err
	}

	s.recording = true
	s.takes++
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
	defer s.finishTake(speech)

	switch activeEngine {
	case config.SpeechRemote:
		return s.stopRemote(speech, remoteCfg)
	case config.SpeechWitAI:
		return s.stopWitAI(speech, lang)
	}

	text, err := speech.Stop()
	if err != nil {
		// Ask for the model again next time: a failed load surfaces here.
		s.mu.Lock()
		s.model = ""
		s.mu.Unlock()
		return "", err
	}

	logger.Info("Dictation transcribed", "characters", len(text))
	return text, nil
}

func (s *Service) stopWitAI(speech speechEngine, lang string) (string, error) {
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

func (s *Service) stopRemote(speech speechEngine, cfg config.SpeechConfig) (string, error) {
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

// Unloads after the last take when the model is not kept. Sent under the lock, so a take
// starting afterwards asks for the model again and its load queues behind the unload.
func (s *Service) finishTake(speech speechEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.takes--
	if s.keepLoaded || s.model == "" || s.takes > 0 {
		return
	}
	s.model = ""
	speech.Unload()
	logger.Info("Speech model unloaded")
}

func (s *Service) Cancel() {
	s.mu.Lock()
	speech, recording := s.speech, s.recording
	s.recording = false
	s.mu.Unlock()

	if speech == nil {
		return
	}
	speech.Cancel()
	if recording {
		s.finishTake(speech)
	}
}

func (s *Service) Close() {
	s.mu.Lock()
	speech := s.speech
	s.speech = nil
	s.recording = false
	s.takes = 0
	s.mu.Unlock()

	if speech != nil {
		speech.Close()
	}
}

func SystemLanguage() string {
	tag, err := locale.GetLocale()
	if err != nil {
		return ""
	}
	tag = strings.ReplaceAll(tag, "_", "-")
	base, _, _ := strings.Cut(tag, "-")
	return strings.ToLower(base)
}
