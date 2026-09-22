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
	// A platform that refused a window is asked again after a while, not on every take.
	retryAfter = 30 * time.Second
)

// surface is the window the frames land in. Every call runs on the UI thread.
type surface interface {
	// Scale is pixels per logical unit; the frame is painted at Width*scale by Height*scale.
	Scale() float64
	Present(frame *image.RGBA)
	Close()
}

// Indicator is the shared window. Each feature drives it through its own Owner, so
// one feature ending never hides what another is still showing. It animates on its
// own goroutine while visible and holds no window at all while it is not.
type Indicator struct {
	runOnMain func(func())
	open      func() (surface, error)
	now       func() time.Time
	interval  time.Duration

	mu      sync.Mutex
	owners  map[*Owner]Phase
	phase   Phase
	level   float32
	running bool
	done    chan struct{}
	closing bool
	// failedAt is when the platform last refused a window; zero when it never has.
	failedAt time.Time
}

// New returns an indicator drawing on the platform surface. runOnMain must run its
// argument on the UI thread and wait for it.
func New(runOnMain func(func())) *Indicator {
	return newIndicator(runOnMain, openSurface, time.Now, frameInterval)
}

func newIndicator(runOnMain func(func()), open func() (surface, error), now func() time.Time, interval time.Duration) *Indicator {
	return &Indicator{
		runOnMain: runOnMain,
		open:      open,
		now:       now,
		interval:  interval,
		owners:    map[*Owner]Phase{},
	}
}

// Owner is one feature's handle on the indicator.
type Owner struct {
	indicator *Indicator
}

func (i *Indicator) Owner() *Owner {
	return &Owner{indicator: i}
}

func (o *Owner) Show(phase Phase)  { o.indicator.claim(o, phase) }
func (o *Owner) Level(rms float32) { o.indicator.Level(rms) }
func (o *Owner) Hide()             { o.indicator.release(o) }

func (i *Indicator) claim(owner *Owner, phase Phase) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closing {
		return
	}
	i.owners[owner] = phase
	i.phase = phase
	i.startLocked()
}

// release forgets the owner; one of the others, if any, sets the phase.
func (i *Indicator) release(owner *Owner) {
	i.mu.Lock()
	defer i.mu.Unlock()

	delete(i.owners, owner)
	for _, phase := range i.owners {
		i.phase = phase
	}
}

func (i *Indicator) Level(rms float32) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.level = loudness(rms)
}

// Close takes the window down at once and waits for it to be gone; nothing is
// shown again afterwards. Called before the window system goes away.
func (i *Indicator) Close() {
	i.mu.Lock()
	i.closing = true
	clear(i.owners)
	done := i.done
	i.mu.Unlock()

	if done != nil {
		<-done
	}
}

func (i *Indicator) startLocked() {
	if i.running || len(i.owners) == 0 {
		return
	}
	if !i.failedAt.IsZero() && i.now().Sub(i.failedAt) < retryAfter {
		return
	}
	i.failedAt = time.Time{}
	i.running = true
	i.done = make(chan struct{})
	go i.animate(i.done)
}

func (i *Indicator) snapshot() (wanted, closing bool, phase Phase, level float32) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.owners) > 0, i.closing, i.phase, i.level
}

// animate owns the surface from the first frame to the end of the fade-out.
func (i *Indicator) animate(done chan struct{}) {
	defer close(done)

	var target surface
	var openErr error
	i.runOnMain(func() { target, openErr = i.open() })
	if openErr != nil {
		logger.Warn("Indicator unavailable", "error", openErr)
		i.mu.Lock()
		i.running = false
		i.failedAt = i.now()
		i.mu.Unlock()
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
		wanted, closing, phase, level := i.snapshot()
		if closing {
			break
		}
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

	// An owner that arrived during the fade-out gets a fresh surface.
	i.mu.Lock()
	i.running = false
	i.startLocked()
	i.mu.Unlock()
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
