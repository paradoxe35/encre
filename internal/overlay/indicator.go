package overlay

import (
	"image"
	"math"
	"sync"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
)

const (
	frameInterval = 33 * time.Millisecond
	fadeIn        = 150 * time.Millisecond
	fadeOut       = 220 * time.Millisecond
	// The level meter rises at once and settles slowly, so speech reads as movement, not flicker.
	levelAttack  = 0.6
	levelRelease = 0.12
)

// surface is the window the frames land in. Every call runs on the UI thread.
type surface interface {
	// Scale is pixels per logical unit; the frame is painted at Width*scale by Height*scale.
	Scale() float64
	Present(frame *image.RGBA)
	Close()
}

// Indicator animates the pill on its own goroutine while it is visible, and holds no
// window at all while it is not.
type Indicator struct {
	runOnMain func(func())
	open      func() (surface, error)
	now       func() time.Time
	interval  time.Duration

	mu      sync.Mutex
	wanted  bool
	phase   Phase
	level   float32
	running bool
	failed  bool
}

// New returns an indicator drawing on the platform surface. runOnMain must run its
// argument on the UI thread and wait for it.
func New(runOnMain func(func())) *Indicator {
	return newIndicator(runOnMain, openSurface, time.Now, frameInterval)
}

func newIndicator(runOnMain func(func()), open func() (surface, error), now func() time.Time, interval time.Duration) *Indicator {
	return &Indicator{runOnMain: runOnMain, open: open, now: now, interval: interval}
}

func (i *Indicator) Show(phase Phase) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.wanted = true
	i.phase = phase
	if i.running || i.failed {
		return
	}
	i.running = true
	go i.animate()
}

func (i *Indicator) Level(rms float32) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.level = loudness(rms)
}

func (i *Indicator) Hide() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.wanted = false
}

func (i *Indicator) snapshot() (wanted bool, phase Phase, level float32) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.wanted, i.phase, i.level
}

func (i *Indicator) stopped(failed bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.running = false
	i.failed = i.failed || failed
}

// animate owns the surface from the first frame to the end of the fade-out.
func (i *Indicator) animate() {
	var target surface
	var openErr error
	i.runOnMain(func() { target, openErr = i.open() })
	if openErr != nil {
		logger.Warn("Dictation indicator unavailable", "error", openErr)
		i.stopped(true)
		return
	}

	scale := target.Scale()
	frame := image.NewRGBA(image.Rect(0, 0, int(Width*scale), int(Height*scale)))
	ticker := time.NewTicker(i.interval)
	defer ticker.Stop()

	started := i.now()
	last := started
	alpha := 0.0
	var trace [traceLen]float32
	for range ticker.C {
		wanted, phase, level := i.snapshot()
		now := i.now()
		dt := now.Sub(last)
		last = now

		alpha = fade(alpha, wanted, dt)
		trace = advance(trace, level)
		Paint(frame, Frame{Phase: phase, T: now.Sub(started).Seconds(), Trace: trace, Alpha: alpha}, scale)
		i.runOnMain(func() { target.Present(frame) })

		if !wanted && alpha == 0 {
			break
		}
	}

	i.runOnMain(target.Close)
	i.stopped(false)

	// A show that arrived during the fade-out starts over with a fresh surface.
	if wanted, phase, _ := i.snapshot(); wanted {
		i.Show(phase)
	}
}

func fade(alpha float64, in bool, dt time.Duration) float64 {
	if in {
		return clamp01(alpha + dt.Seconds()/fadeIn.Seconds())
	}
	return clamp01(alpha - dt.Seconds()/fadeOut.Seconds())
}

// advance shifts the trace along and follows the new level at its end.
func advance(trace [traceLen]float32, level float32) [traceLen]float32 {
	copy(trace[:], trace[1:])
	trace[traceLen-1] = follow(trace[traceLen-1], level)
	return trace
}

// Speech RMS lives a few percent above silence, so a straight meter would barely
// move; mapping decibels from -45 (quiet) to -12 (loud) onto 0..1 gives it life.
func loudness(rms float32) float32 {
	if rms <= 0 {
		return 0
	}
	db := 20 * math.Log10(float64(rms))
	return float32(clamp01((db + 45) / 33))
}

func follow(current, target float32) float32 {
	if target > current {
		return current + (target-current)*levelAttack
	}
	return current + (target-current)*levelRelease
}
