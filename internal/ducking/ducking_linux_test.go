package ducking

import (
	"errors"
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

// ducked is the slider a lowering leaves a stream at.
func ducked(volume proto.Volume) proto.Volume {
	return proto.Volume(float64(volume) * sliderShare(gain))
}

func TestRestoredVolumesPutsBackTheSameStream(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{60000, 60000}}}
	got, ok := restoredVolumes(saved, pulseStream{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{ducked(60000), ducked(60000)}})
	if !ok || got[0] != 60000 || got[1] != 60000 {
		t.Fatalf("got %v, %v", got, ok)
	}
}

// The sound server gives a restarted player the volume it was left at, so its new stream is raised too.
func TestRestoredVolumesPutsBackTheSameAppsNewStream(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{60000, 60000}}}
	if _, ok := restoredVolumes(saved, pulseStream{Index: 9, Process: "50", App: "Firefox", Volumes: proto.ChannelVolumes{ducked(60000), ducked(60000)}}); !ok {
		t.Fatal("the same app's new stream was left lowered")
	}
	if _, ok := restoredVolumes(saved, pulseStream{Index: 9, Process: "50", App: "mpv", Volumes: proto.ChannelVolumes{ducked(60000), ducked(60000)}}); ok {
		t.Fatal("another app's stream was changed")
	}
}

func TestRestoredVolumesNeverLowers(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{30000}}}
	if _, ok := restoredVolumes(saved, pulseStream{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{50000}}); ok {
		t.Fatal("a stream turned up since was lowered")
	}
}

func TestRestoreStreamsKeepsTheAppsNotPlayingYet(t *testing.T) {
	saved := []pulseStream{
		{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{60000, 60000}},
		{Index: 8, Process: "43", App: "Spotify", Volumes: proto.ChannelVolumes{50000, 50000}},
		{Index: 9, Process: "44", Volumes: proto.ChannelVolumes{50000}},
	}
	live := []pulseStream{{Index: 12, Process: "60", App: "Firefox", Volumes: proto.ChannelVolumes{ducked(60000), ducked(60000)}}}

	var set []uint32
	left, err := restoreStreams(saved, live, func(index uint32, _ proto.ChannelVolumes) error {
		set = append(set, index)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 1 || set[0] != 12 {
		t.Fatalf("raised %v, want Firefox's new stream", set)
	}
	if len(left) != 1 || left[0].App != "Spotify" {
		t.Fatalf("left %v, want Spotify kept and the nameless stream dropped", left)
	}
}

func TestRestoreStreamsKeepsAStreamItFailedToRaise(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{60000}}}
	live := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{ducked(60000)}}}

	left, err := restoreStreams(saved, live, func(uint32, proto.ChannelVolumes) error { return errors.New("refused") })
	if err == nil || len(left) != 1 {
		t.Fatalf("left %v, %v; want the stream kept and the failure reported", left, err)
	}
}

// A stream turned up since is reached, just not changed.
func TestRestoreStreamsCountsALouderStreamAsDone(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{30000}}}
	live := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{50000}}}

	left, err := restoreStreams(saved, live, func(uint32, proto.ChannelVolumes) error {
		t.Error("a louder stream was changed")
		return nil
	})
	if err != nil || len(left) != 0 {
		t.Fatalf("left %v, %v", left, err)
	}
}

// After a crash the user may have set the player again; a level away from the ducked one is theirs.
func TestRestoredVolumesLeavesALevelChosenSince(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{60000}}}
	for _, now := range []proto.Volume{0, ducked(60000) / 2, 45000} {
		if _, ok := restoredVolumes(saved, pulseStream{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{now}}); ok {
			t.Errorf("a stream set to %d since was raised", now)
		}
	}
	if _, ok := restoredVolumes(saved, pulseStream{Index: 7, Process: "42", App: "Firefox", Volumes: proto.ChannelVolumes{ducked(60000) + 300}}); !ok {
		t.Error("a stream a rounding off its ducked level was left lowered")
	}
}

func TestRestoreStreamsDropsAStreamSetSince(t *testing.T) {
	saved := []pulseStream{{Index: 7, Process: "42", App: "Spotify", Volumes: proto.ChannelVolumes{60000, 60000}}}
	live := []pulseStream{{Index: 12, Process: "60", App: "Spotify", Volumes: proto.ChannelVolumes{20000, 20000}}}

	left, err := restoreStreams(saved, live, func(uint32, proto.ChannelVolumes) error {
		t.Error("a stream set since was changed")
		return nil
	})
	if err != nil || len(left) != 0 {
		t.Fatalf("left %v, %v; want the stream counted as reached", left, err)
	}
}
