// Package ducking turns down what other apps play while the microphone is open, and back up after.
package ducking

import (
	"math"
	"sync"

	"github.com/paradoxe35/encre/internal/logger"
)

// Other audio drops by 18 dB: clearly in the background, still there.
var (
	gain       = math.Pow(10, -18.0/20)
	sliderGain = math.Cbrt(gain)
)

// lowerFunc turns other audio down and returns what puts it back.
type lowerFunc func() (restore func() error, err error)

// Ducker restores only what it lowered, at the level it found it.
type Ducker struct {
	mu      sync.Mutex
	lower   lowerFunc
	restore func() error
}

func New() *Ducker {
	return &Ducker{lower: lower}
}

func (d *Ducker) Lower() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.restore != nil {
		return
	}

	restore, err := d.lower()
	if err != nil {
		logger.Warn("Could not lower other audio", "error", err)
		return
	}
	d.restore = restore
}

func (d *Ducker) Restore() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.restore == nil {
		return
	}

	if err := d.restore(); err != nil {
		logger.Warn("Could not restore other audio", "error", err)
	}
	d.restore = nil
}
