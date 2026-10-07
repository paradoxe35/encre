package ducking

import (
	"testing"

	"github.com/jfreymuth/pulse/proto"
)

func TestScaledKeepsEachChannel(t *testing.T) {
	got := scaled(proto.ChannelVolumes{proto.VolumeNorm, proto.VolumeNorm / 2}, 0.5)
	if len(got) != 2 || got[0] != proto.VolumeNorm/2 || got[1] != proto.VolumeNorm/4 {
		t.Fatalf("got %v", got)
	}
}
