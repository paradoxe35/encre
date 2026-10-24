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

func TestRestoredVolumesPutsBackTheSameStream(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{60000, 60000}}}
	got, ok := restoredVolumes(saved, pulseStream{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{8000, 8000}})
	if !ok || got[0] != 60000 || got[1] != 60000 {
		t.Fatalf("got %v, %v", got, ok)
	}
}

// The sound server gives a restarted player the volume it was left at, so its new stream is raised too.
func TestRestoredVolumesPutsBackTheSameAppsNewStream(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{60000, 60000}}}
	if _, ok := restoredVolumes(saved, pulseStream{Index: 9, Process: "50", App: "Firefox", Volumes: proto.ChannelVolumes{8000, 8000}}); !ok {
		t.Fatal("the same app's new stream was left lowered")
	}
	if _, ok := restoredVolumes(saved, pulseStream{Index: 9, Process: "50", App: "mpv", Volumes: proto.ChannelVolumes{8000, 8000}}); ok {
		t.Fatal("another app's stream was changed")
	}
}

func TestRestoredVolumesNeverLowers(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{30000}}}
	if _, ok := restoredVolumes(saved, pulseStream{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{50000}}); ok {
		t.Fatal("a stream turned up since was lowered")
	}
}
