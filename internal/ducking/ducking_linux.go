package ducking

import (
	"errors"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

const requestTimeout = time.Second

// Via the sound server (PipeWire too), so players are ducked without supporting remote control.
type pulseSession struct {
	client *pulse.Client
	found  map[uint32]proto.ChannelVolumes
}

func open() (session, error) {
	client, err := pulse.NewClient(pulse.ClientApplicationName("Encre"), pulse.ClientTimeout(requestTimeout))
	if err != nil {
		return nil, err
	}

	var streams proto.GetSinkInputInfoListReply
	if err := client.RawRequest(&proto.GetSinkInputInfoList{}, &streams); err != nil {
		client.Close()
		return nil, err
	}

	own := strconv.Itoa(os.Getpid())
	found := map[uint32]proto.ChannelVolumes{}
	for _, stream := range streams {
		if !stream.Corked && stream.VolumeWritable && stream.Properties["application.process.id"].String() != own {
			found[stream.SinkInputIndex] = stream.ChannelVolumes
		}
	}
	return &pulseSession{client: client, found: found}, nil
}

// A stream that ended meanwhile has nothing left to change, so it is dropped rather than reported.
func (s *pulseSession) scale(share float64) error {
	var errs []error
	for index, volumes := range s.found {
		request := &proto.SetSinkInputVolume{SinkInputIndex: index, ChannelVolumes: scaled(volumes, sliderShare(share))}
		err := s.client.RawRequest(request, nil)
		switch {
		case errors.Is(err, proto.ErrNoSuchEntity):
			delete(s.found, index)
		case err != nil:
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *pulseSession) close() { s.client.Close() }

// The sound server's volumes are cubic, so an amplitude share is its cube root.
func sliderShare(share float64) float64 { return math.Cbrt(share) }

func scaled(volumes proto.ChannelVolumes, by float64) proto.ChannelVolumes {
	quieter := make(proto.ChannelVolumes, len(volumes))
	for i, volume := range volumes {
		quieter[i] = proto.Volume(float64(volume) * by)
	}
	return quieter
}
