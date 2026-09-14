package revision

import (
	"strings"
	"sync"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/stt"
)

// Dictation turns push-to-talk edges into typed text.
type Dictation struct {
	service   *stt.Service
	processor *Processor
	config    func() *config.Config
	report    func(error)

	mu      sync.Mutex
	running bool
}

func NewDictation(processor *Processor, current func() *config.Config, report func(error)) *Dictation {
	return &Dictation{
		service:   stt.NewService(),
		processor: processor,
		config:    current,
		report:    report,
	}
}

func (d *Dictation) Service() *stt.Service { return d.service }

// Prepare loads the model without opening the microphone. Audio is opened only
// when the dictate shortcut starts recording.
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

// Toggle is the hotkey edge: down starts, up stops and types.
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

	go func() {
		raw, err := d.service.StopRecording()
		if err != nil {
			d.fail(err)
			return
		}
		logger.Info("Dictation finished", "characters", len(raw))
		if strings.TrimSpace(raw) == "" {
			return
		}
		text := raw
		if d.config().SpeechSettings().CleanUp {
			if cleaned, err := d.processor.CleanTranscript(raw); err == nil {
				text = cleaned
			} else {
				d.fail(err)
				return
			}
		}
		if err := d.processor.InsertText(text); err != nil {
			d.fail(err)
			return
		}
		d.processor.RecordSpeech(raw, text)
	}()
}

func (d *Dictation) Cancel() {
	d.mu.Lock()
	d.running = false
	d.mu.Unlock()
	d.service.Cancel()
}

func (d *Dictation) Close() { d.service.Close() }

func (d *Dictation) fail(err error) {
	logger.Error("Dictation failed", "error", err)
	if d.report != nil {
		d.report(err)
	}
}
