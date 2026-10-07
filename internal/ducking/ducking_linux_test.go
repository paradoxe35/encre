package ducking

import (
	"math"
	"testing"

	"github.com/jfreymuth/pulse/proto"
)

func TestScaledKeepsEachChannel(t *testing.T) {
	got := scaled(proto.ChannelVolumes{proto.VolumeNorm, proto.VolumeNorm / 2}, 0.5)
	if len(got) != 2 || got[0] != proto.VolumeNorm/2 || got[1] != proto.VolumeNorm/4 {
		t.Fatalf("got %v", got)
	}
}

// The sound server's volumes are cubic: a slider at the cube root of a share plays at that share.
func TestSliderShareLandsOnTheAmplitude(t *testing.T) {
	if got := math.Pow(sliderShare(gain), 3); math.Abs(got-gain) > 1e-9 {
		t.Fatalf("amplitude %v, want %v", got, gain)
	}
}
