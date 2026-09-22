package revision

import (
	"errors"
	"strings"
	"sync"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/stt"
)

var ErrNoSpeech = errors.New("no speech was recognised - check the microphone under Settings > Speech")

// speechService is what dictation needs from the recorder, so it can be tested without a microphone.
type speechService interface {
	Prepare(cfg config.SpeechConfig) error
	StartRecording(cfg config.SpeechConfig) error
	StopRecording() (string, error)
	Close()
}

// typist is what dictation needs from the processor, so it can be tested without a clipboard.
type typist interface {
	CleanTranscript(text string) (string, error)
	InsertText(text string) error
	RecordSpeech(raw, final string)
}

type Dictation struct {
	service speechService
	typist  typist
	config  func() *config.Config
	report  func(error)

	mu      sync.Mutex
	running bool
	order   sequence
}

// sequence types takes in spoken order, however long each transcription takes.
type sequence struct {
	mu   sync.Mutex
	last chan struct{}
}

// claim returns the gate for the take before this one, and the release for this
// one. A nil gate means nothing is ahead.
func (s *sequence) claim() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ahead := s.last
	mine := make(chan struct{})
	s.last = mine

	return ahead, func() { close(mine) }
}

func NewDictation(processor *Processor, current func() *config.Config, report func(error)) *Dictation {
	return newDictation(stt.NewService(), processor, current, report)
}

func newDictation(service speechService, typist typist, current func() *config.Config, report func(error)) *Dictation {
	return &Dictation{
		service: service,
		typist:  typist,
		config:  current,
		report:  report,
	}
}

// Loads the model without opening the microphone; audio opens only when recording starts.
func (d *Dictation) Prepare() {
	cfg := d.config()
	if !cfg.SpeechReady() {
		return
	}

	go func() {
		if err := d.service.Prepare(cfg.SpeechSettings()); err != nil {
			logger.Info("Dictation not ready yet", "reason", err)
		}
	}()
}

func (d *Dictation) Toggle(down bool) {
	if down {
		d.start()
		return
	}
	d.stop()
}

func (d *Dictation) start() {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()

	if err := d.service.StartRecording(d.config().SpeechSettings()); err != nil {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		d.fail(err)
		return
	}
	logger.Info("Dictation started")
}

func (d *Dictation) stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	d.mu.Unlock()

	ahead, typed := d.order.claim()

	go func() {
		defer typed()

		// Ends the capture straight away: the recorder cannot take the next
		// press until this one is stopped.
		raw, err := d.service.StopRecording()

		if ahead != nil {
			<-ahead
		}
		if err != nil {
			d.fail(err)
			return
		}

		logger.Info("Dictation finished", "characters", len(raw))
		if strings.TrimSpace(raw) == "" {
			d.fail(ErrNoSpeech)
			return
		}

		text := raw
		if d.config().SpeechSettings().CleanUp {
			if cleaned, err := d.typist.CleanTranscript(raw); err == nil {
				text = cleaned
			} else {
				d.fail(err)
				return
			}
		}
		if err := d.typist.InsertText(text); err != nil {
			d.fail(err)
			return
		}
		d.typist.RecordSpeech(raw, text)
	}()
}

func (d *Dictation) Close() { d.service.Close() }

func (d *Dictation) fail(err error) {
	logger.Error("Dictation failed", "error", err)
	d.report(err)
}
