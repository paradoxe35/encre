package ducking

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

const requestTimeout = time.Second

// Via the sound server (PipeWire too), so players are ducked without supporting remote control.
type pulseSession struct {
	client  *pulse.Client
	streams map[uint32]pulseStream
}

// pulseStream is a player's stream as found, enough to recognise it again after a crash.
type pulseStream struct {
	Index   uint32               `json:"index"`
	Process string               `json:"process"`
	App     string               `json:"app"`
	Volumes proto.ChannelVolumes `json:"volumes"`
}

func open() (session, error) {
	client, streams, err := sinkInputs()
	if err != nil {
		return nil, err
	}

	own := strconv.Itoa(os.Getpid())
	found := map[uint32]pulseStream{}
	for _, stream := range streams {
		if !stream.Corked && stream.VolumeWritable && stream.Properties["application.process.id"].String() != own {
			found[stream.SinkInputIndex] = streamOf(stream)
		}
	}
	return &pulseSession{client: client, streams: found}, nil
}

func sinkInputs() (*pulse.Client, proto.GetSinkInputInfoListReply, error) {
	client, err := pulse.NewClient(pulse.ClientApplicationName("Encre"), pulse.ClientTimeout(requestTimeout))
	if err != nil {
		return nil, nil, err
	}

	var streams proto.GetSinkInputInfoListReply
	if err := client.RawRequest(&proto.GetSinkInputInfoList{}, &streams); err != nil {
		client.Close()
		return nil, nil, err
	}
	return client, streams, nil
}

func streamOf(stream *proto.GetSinkInputInfoReply) pulseStream {
	return pulseStream{
		Index:   stream.SinkInputIndex,
		Process: stream.Properties["application.process.id"].String(),
		App:     stream.Properties["application.name"].String(),
		Volumes: stream.ChannelVolumes,
	}
}

// A stream that ended meanwhile has nothing left to change, so it is dropped rather than reported.
func (s *pulseSession) scale(share float64) error {
	var errs []error
	for index, stream := range s.streams {
		request := &proto.SetSinkInputVolume{SinkInputIndex: index, ChannelVolumes: scaled(stream.Volumes, sliderShare(share))}
		err := s.client.RawRequest(request, nil)
		switch {
		case errors.Is(err, proto.ErrNoSuchEntity):
			delete(s.streams, index)
		case err != nil:
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *pulseSession) found() any {
	streams := make([]pulseStream, 0, len(s.streams))
	for _, stream := range s.streams {
		streams = append(streams, stream)
	}
	return streams
}

func (s *pulseSession) close() { s.client.Close() }

func restore(data []byte) error {
	var saved []pulseStream
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	if len(saved) == 0 {
		return nil
	}

	client, streams, err := sinkInputs()
	if err != nil {
		return err
	}
	defer client.Close()

	var errs []error
	for _, stream := range streams {
		volumes, ok := restoredVolumes(saved, streamOf(stream))
		if !ok {
			continue
		}
		request := &proto.SetSinkInputVolume{SinkInputIndex: stream.SinkInputIndex, ChannelVolumes: volumes}
		if err := client.RawRequest(request, nil); err != nil && !errors.Is(err, proto.ErrNoSuchEntity) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// The same stream, or the same app's new one, which the sound server gives the volume it last had.
// Only ever raised: a stream louder than saved was turned up since, and is left as it is.
func restoredVolumes(saved []pulseStream, now pulseStream) (proto.ChannelVolumes, bool) {
	match := slices.IndexFunc(saved, func(was pulseStream) bool {
		return was.Index == now.Index && was.Process == now.Process
	})
	if match < 0 && now.App != "" {
		match = slices.IndexFunc(saved, func(was pulseStream) bool {
			return was.App == now.App && len(was.Volumes) == len(now.Volumes)
		})
	}
	if match < 0 || len(now.Volumes) == 0 || len(saved[match].Volumes) != len(now.Volumes) {
		return nil, false
	}
	was := saved[match].Volumes
	if slices.Max(now.Volumes) >= slices.Max(was) {
		return nil, false
	}
	return was, true
}

// The sound server's volumes are cubic, so an amplitude share is its cube root.
func sliderShare(share float64) float64 { return math.Cbrt(share) }

func scaled(volumes proto.ChannelVolumes, by float64) proto.ChannelVolumes {
	quieter := make(proto.ChannelVolumes, len(volumes))
	for i, volume := range volumes {
		quieter[i] = proto.Volume(float64(volume) * by)
	}
	return quieter
}
