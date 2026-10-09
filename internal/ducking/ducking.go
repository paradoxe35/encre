// Package ducking turns down what other apps play while the microphone is open, and back up after.
package ducking

import (
	"math"
	"runtime"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
)

// Other audio drops by 18 dB: clearly in the background, still there.
var gain = math.Pow(10, -18.0/20)

const (
	fadeDown = 400 * time.Millisecond
	fadeUp   = 800 * time.Millisecond
	step     = 20 * time.Millisecond
)

// session is the other audio found playing when lowering began.
type session interface {
	// scale sets every stream to share of the amplitude it had when found; 1 puts it back.
	scale(share float64) error
	close()
}

type wish struct {
	lowered bool
	// done, when set, asks for the audio back at once and is closed once it is.
	done chan struct{}
}

// Fades run on a worker so nothing waits; a mid-fade wish turns it around from where it is.
type Ducker struct {
	open  func() (session, error)
	step  time.Duration
	wants chan wish
}

func New() *Ducker {
	return newDucker(open, step)
}

func newDucker(open func() (session, error), step time.Duration) *Ducker {
	d := &Ducker{open: open, step: step, wants: make(chan wish, 1)}
	go d.run()
	return d
}

func (d *Ducker) Lower()   { d.want(wish{lowered: true}) }
func (d *Ducker) Restore() { d.want(wish{}) }

// Close puts the audio back without a fade and waits for it, so quitting never leaves it low.
func (d *Ducker) Close() {
	done := make(chan struct{})
	d.want(wish{done: done})
	select {
	case <-done:
	case <-time.After(time.Second):
		logger.Warn("Other audio may still be lowered")
	}
}

// want replaces a wish the worker has not taken yet: only the latest one matters.
func (d *Ducker) want(w wish) {
	select {
	case <-d.wants:
	default:
	}
	d.wants <- w
}

func (d *Ducker) run() {
	// Windows audio objects belong to the thread that made them.
	runtime.LockOSThread()

	var current session
	share := 1.0
	for w := range d.wants {
		for {
			if w.lowered && current == nil {
				opened, err := d.open()
				if err != nil {
					logger.Warn("Could not lower other audio", "error", err)
					break
				}
				current, share = opened, 1
			}
			if current == nil {
				break
			}

			if w.done != nil {
				d.apply(current, 1)
				current.close()
				current = nil
				break
			}

			target, over := 1.0, fadeUp
			if w.lowered {
				target, over = gain, fadeDown
			}
			next, interrupted := d.fade(current, &share, target, over)
			if interrupted {
				w = next
				continue
			}
			if !w.lowered {
				current.close()
				current = nil
			}
			break
		}
		if w.done != nil {
			close(w.done)
		}
	}
}

// fade moves share to target over the given time and reports a newer wish that cut it short.
func (d *Ducker) fade(current session, share *float64, target float64, over time.Duration) (wish, bool) {
	from := *share
	steps := max(int(over/d.step), 1)
	for i := 1; i <= steps; i++ {
		select {
		case next := <-d.wants:
			return next, true
		case <-time.After(d.step):
		}
		*share = between(from, target, float64(i)/float64(steps))
		d.apply(current, *share)
	}
	return wish{}, false
}

func (d *Ducker) apply(current session, share float64) {
	if err := current.scale(share); err != nil {
		logger.Warn("Could not change other audio", "error", err)
	}
}

// Eases evenly in decibels, as loudness is heard, and slowly at both ends, so it never steps.
func between(from, to, t float64) float64 {
	if t >= 1 {
		return to
	}
	eased := t * t * (3 - 2*t)
	fromDB, toDB := 20*math.Log10(from), 20*math.Log10(to)
	return math.Pow(10, (fromDB+(toDB-fromDB)*eased)/20)
}
