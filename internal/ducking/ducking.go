// Package ducking turns down what other apps play while the microphone is open, and back up after.
package ducking

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/utils"
)

// Other audio drops by 18 dB: clearly in the background, still there.
var gain = math.Pow(10, -18.0/20)

// A restore raises only what still sits within this of its ducked level; further off, someone chose it since.
const duckedSlackDB = 3.0

const (
	fadeDown = 400 * time.Millisecond
	fadeUp   = 800 * time.Millisecond
	step     = 20 * time.Millisecond
)

// session is the other audio found playing when lowering began.
type session interface {
	// scale sets every stream to share of the amplitude it had when found; 1 puts it back.
	scale(share float64) error
	// found is what restoreFound needs to put the levels back if this process dies lowered.
	found() any
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
				retryPending()
				opened, err := d.open()
				if err != nil {
					logger.Warn("Could not lower other audio", "error", err)
					break
				}
				current, share = opened, 1
				save(current)
			}
			if current == nil {
				break
			}

			if w.done != nil {
				d.apply(current, 1)
				release(current)
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
				release(current)
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

// share is the amplitude now over the amplitude found when lowering began.
func stillDucked(share float64) bool {
	return math.Abs(20*math.Log10(share/gain)) <= duckedSlackDB
}

func release(current session) {
	current.close()
	forget()
}

var (
	statePath    = func() string { return utils.AppHomeDir("ducked-audio.json") }
	pendingPath  = func() string { return utils.AppHomeDir("unrestored-audio.json") }
	restoreFound = restore
)

// An app not open again since the crash gets its level back whenever it returns within this.
const keepPending = 7 * 24 * time.Hour

// pending is a crash's levels that restoreFound could not reach yet.
type pending struct {
	Saved time.Time       `json:"saved"`
	Found json.RawMessage `json:"found"`
}

// held counts this process's live ducks, whose saved levels are not a crash's leftover.
var state struct {
	sync.Mutex
	held int
}

func save(current session) {
	state.Lock()
	defer state.Unlock()
	state.held++

	data, err := json.Marshal(current.found())
	if err == nil {
		err = writePrivate(statePath(), data)
	}
	if err != nil {
		logger.Warn("Could not save the audio levels to restore after a crash", "error", err)
	}
}

func forget() {
	state.Lock()
	defer state.Unlock()
	state.held--
	if state.held > 0 {
		return
	}
	if err := os.Remove(statePath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.Warn("Could not remove the saved audio levels", "error", err)
	}
}

// Renamed into place, so a crash mid-write never leaves half a file to restore from.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RecoverStale puts back other audio that a crashed or killed run left lowered.
// What it cannot reach yet, such as an app not open again, is kept and tried here and before each lowering.
// It does nothing when no levels were left behind or a duck in this process is holding them.
func RecoverStale() error {
	state.Lock()
	defer state.Unlock()
	if state.held > 0 {
		return nil
	}
	return recoverLocked()
}

// Before opening, so the levels found are not ones a retry is about to raise.
func retryPending() {
	state.Lock()
	defer state.Unlock()
	if _, err := os.Stat(pendingPath()); state.held > 0 || err != nil {
		return
	}
	if err := recoverLocked(); err != nil {
		logger.Warn("Could not restore audio an earlier run left lowered", "error", err)
	}
}

func recoverLocked() error {
	left, err := readPending()
	if err != nil {
		return err
	}
	crashed, err := readCrashed()
	if err != nil {
		return err
	}
	if crashed != nil {
		left = append(left, *crashed)
	}

	var errs []error
	kept := left[:0]
	for _, p := range left {
		if time.Since(p.Saved) > keepPending {
			continue
		}
		rest, err := restoreFound(p.Found)
		if err != nil {
			errs = append(errs, err)
		}
		if len(rest) > 0 {
			kept = append(kept, pending{Saved: p.Saved, Found: rest})
		}
	}

	if err := writePending(kept); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if crashed != nil {
		if err := os.Remove(statePath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// A file that does not parse is dropped: there is nothing in it to restore.
func readPending() ([]pending, error) {
	data, err := os.ReadFile(pendingPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var left []pending
	if err := json.Unmarshal(data, &left); err != nil {
		logger.Warn("Dropping unreadable audio levels left to restore", "error", err)
		return nil, nil
	}
	return left, nil
}

// The file's time is when the crashed run lowered the audio.
func readCrashed() (*pending, error) {
	info, err := os.Stat(statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(statePath())
	if err != nil {
		return nil, err
	}
	return &pending{Saved: info.ModTime(), Found: data}, nil
}

func writePending(left []pending) error {
	if len(left) == 0 {
		if err := os.Remove(pendingPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(left)
	if err != nil {
		return err
	}
	return writePrivate(pendingPath(), data)
}

// leftOver is what a restore hands back as still lowered: nothing once every level was reached.
func leftOver[T any](levels []T) ([]byte, error) {
	if len(levels) == 0 {
		return nil, nil
	}
	return json.Marshal(levels)
}
