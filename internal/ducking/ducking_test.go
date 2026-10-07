package ducking

import (
	"errors"
	"math"
	"testing"
)

type fakeSystem struct {
	lowered, restored int
	fail              error
}

func (f *fakeSystem) lower() (func() error, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	f.lowered++
	return func() error { f.restored++; return nil }, nil
}

func ducker(system *fakeSystem) *Ducker {
	return &Ducker{lower: system.lower}
}

func TestLowerOnceRestoreOnce(t *testing.T) {
	system := &fakeSystem{}
	d := ducker(system)

	d.Lower()
	d.Lower()
	d.Restore()
	d.Restore()

	if system.lowered != 1 || system.restored != 1 {
		t.Fatalf("lowered %d, restored %d; want one of each", system.lowered, system.restored)
	}
}

func TestNothingIsRestoredThatWasNotLowered(t *testing.T) {
	system := &fakeSystem{fail: errors.New("no sound server")}
	d := ducker(system)

	d.Lower()
	d.Restore()
	if system.restored != 0 {
		t.Fatal("restored after a failed lower")
	}

	system.fail = nil
	d.Lower()
	if system.lowered != 1 {
		t.Fatal("a failed lower blocked the next one")
	}
}

// Volumes set on a slider and applied to amplitude must land on the same drop.
func TestBothScalesDropTheSameAmount(t *testing.T) {
	db := func(amplitude float64) float64 { return 20 * math.Log10(amplitude) }
	if got := db(gain); math.Abs(got+18) > 0.01 {
		t.Fatalf("amplitude drop is %.2f dB", got)
	}
	if got := db(math.Pow(sliderGain, 3)); math.Abs(got+18) > 0.01 {
		t.Fatalf("slider drop is %.2f dB", got)
	}
}
