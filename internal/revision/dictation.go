package revision

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/ducking"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/overlay"
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

// assistant is what dictation needs from the processor, so it can be tested without a clipboard.
type assistant interface {
	CleanTranscript(text string) (string, error)
	InsertText(text string) error
	RecordSpeech(raw, final string)
	Ask(ctx context.Context, question string, onText func(string)) (string, error)
}

type otherAudio interface {
	Lower()
	Restore()
	Close()
}

type Dictation struct {
	service   speechService
	assistant assistant
	config    func() *config.Config
	report    func(config.ActionKind, error)
	answers   answerView
	audio     otherAudio

	mu sync.Mutex
	// recording is the action the open take belongs to, or "" when the microphone is closed.
	recording config.ActionKind
	indicator overlay.Overlay
	// pending counts takes still transcribing; the indicator stays until they are typed.
	pending int
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

func NewDictation(processor *Processor, current func() *config.Config,
	report func(config.ActionKind, error), answers answerView) *Dictation {
	return newDictation(stt.NewService(), processor, current, report, answers)
}

func newDictation(service speechService, assistant assistant, current func() *config.Config,
	report func(config.ActionKind, error), answers answerView) *Dictation {
	return &Dictation{
		service:   service,
		assistant: assistant,
		config:    current,
		report:    report,
		answers:   answers,
		audio:     ducking.New(),
		indicator: overlay.Disabled{},
	}
}

// SetOverlay swaps the indicator; the old one is hidden in case it was showing.
func (d *Dictation) SetOverlay(indicator overlay.Overlay) {
	d.mu.Lock()
	previous := d.indicator
	d.indicator = indicator
	d.mu.Unlock()
	previous.Hide()
}

func (d *Dictation) Level(level float32) {
	d.overlay().Level(level)
}

func (d *Dictation) overlay() overlay.Overlay {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.indicator
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

func (d *Dictation) Toggle(down bool) { d.hold(config.ActionDictate, down) }

func (d *Dictation) Ask(down bool) { d.hold(config.ActionAsk, down) }

func (d *Dictation) Recording(kind config.ActionKind) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recording == kind
}

func (d *Dictation) hold(kind config.ActionKind, down bool) {
	if down {
		d.start(kind)
		return
	}
	d.stop(kind)
}

func (d *Dictation) start(kind config.ActionKind) {
	d.mu.Lock()
	if d.recording != "" {
		d.mu.Unlock()
		return
	}
	d.recording = kind
	d.mu.Unlock()

	speech := d.config().SpeechSettings()
	if speech.LowersAudio() {
		d.audio.Lower()
	}
	if err := d.service.StartRecording(speech); err != nil {
		d.mu.Lock()
		d.recording = ""
		d.mu.Unlock()
		d.audio.Restore()
		d.fail(kind, err)
		return
	}

	// A release that beat us here has already moved the indicator on.
	d.mu.Lock()
	if d.recording == kind {
		d.indicator.Show(overlay.Listening)
	}
	d.mu.Unlock()
	logger.Info("Recording started", "action", kind)
}

func (d *Dictation) stop(kind config.ActionKind) {
	d.mu.Lock()
	if d.recording != kind {
		d.mu.Unlock()
		return
	}
	d.recording = ""
	d.pending++
	d.mu.Unlock()

	d.audio.Restore()
	d.overlay().Show(overlay.Transcribing)
	ahead, done := d.order.claim()

	go func() {
		defer done()
		settle := sync.OnceFunc(d.settle)
		defer settle()

		// Ends the capture straight away: the recorder cannot take the next
		// press until this one is stopped.
		raw, err := d.service.StopRecording()

		if ahead != nil {
			<-ahead
		}
		if err != nil {
			d.fail(kind, err)
			return
		}

		logger.Info("Recording transcribed", "action", kind, "characters", len(raw))
		if strings.TrimSpace(raw) == "" {
			d.fail(kind, ErrNoSpeech)
			return
		}

		if kind == config.ActionAsk {
			err = d.answer(raw, settle)
		} else {
			err = d.write(raw)
		}
		if err != nil {
			d.fail(kind, err)
		}
	}()
}

func (d *Dictation) write(raw string) error {
	text := raw
	if d.config().SpeechSettings().CleanUp {
		cleaned, err := d.assistant.CleanTranscript(raw)
		if err != nil {
			return err
		}
		text = cleaned
	}
	if err := d.assistant.InsertText(text); err != nil {
		return err
	}
	d.assistant.RecordSpeech(raw, text)
	return nil
}

// The indicator gives way to the answer at its first words.
func (d *Dictation) answer(question string, settle func()) error {
	return streamAnswer(d.assistant.Ask, d.answers, strings.TrimSpace(question), settle)
}

// settle hides the indicator once nothing is recording or transcribing any more.
func (d *Dictation) settle() {
	d.mu.Lock()
	d.pending--
	idle := d.pending == 0 && d.recording == ""
	indicator := d.indicator
	d.mu.Unlock()

	if idle {
		indicator.Hide()
	}
}

func (d *Dictation) Close() {
	d.audio.Close()
	d.service.Close()
}

func (d *Dictation) fail(kind config.ActionKind, err error) {
	logger.Error("Voice action failed", "action", kind, "error", err)
	d.report(kind, err)
}
