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

// restore hands back the streams it could not reach yet, for a later try.
func restore(data []byte) ([]byte, error) {
	var saved []pulseStream
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return nil, nil
	}

	client, streams, err := sinkInputs()
	if err != nil {
		return data, err
	}
	defer client.Close()

	live := make([]pulseStream, 0, len(streams))
	for _, stream := range streams {
		live = append(live, streamOf(stream))
	}
	left, err := restoreStreams(saved, live, func(index uint32, volumes proto.ChannelVolumes) error {
		err := client.RawRequest(&proto.SetSinkInputVolume{SinkInputIndex: index, ChannelVolumes: volumes}, nil)
		if errors.Is(err, proto.ErrNoSuchEntity) {
			return nil
		}
		return err
	})
	rest, marshalErr := leftOver(left)
	return rest, errors.Join(err, marshalErr)
}

// A saved stream no live one matches stays lowered for when its app plays again,
// unless it has no app name to be recognised by once its process is gone.
func restoreStreams(saved, live []pulseStream, set func(uint32, proto.ChannelVolumes) error) ([]pulseStream, error) {
	reached := make([]bool, len(saved))
	var errs []error
	for _, now := range live {
		match := savedMatch(saved, now)
		if match < 0 {
			continue
		}
		reached[match] = true
		volumes, ok := raised(saved[match].Volumes, now.Volumes)
		if !ok {
			continue
		}
		if err := set(now.Index, volumes); err != nil {
			reached[match] = false
			errs = append(errs, err)
		}
	}

	var left []pulseStream
	for i, was := range saved {
		if !reached[i] && was.App != "" {
			left = append(left, was)
		}
	}
	return left, errors.Join(errs...)
}

// The same stream, or the same app's new one, which the sound server gives the volume it last had.
func restoredVolumes(saved []pulseStream, now pulseStream) (proto.ChannelVolumes, bool) {
	match := savedMatch(saved, now)
	if match < 0 {
		return nil, false
	}
	return raised(saved[match].Volumes, now.Volumes)
}

func savedMatch(saved []pulseStream, now pulseStream) int {
	match := slices.IndexFunc(saved, func(was pulseStream) bool {
		return was.Index == now.Index && was.Process == now.Process
	})
	if match < 0 && now.App != "" {
		match = slices.IndexFunc(saved, func(was pulseStream) bool {
			return was.App == now.App && len(was.Volumes) == len(now.Volumes)
		})
	}
	return match
}

// Only a stream still at its ducked level is raised: one set elsewhere since is left as it is.
func raised(was, now proto.ChannelVolumes) (proto.ChannelVolumes, bool) {
	if len(now) == 0 || len(was) != len(now) || slices.Max(was) == 0 {
		return nil, false
	}
	if !stillDucked(math.Pow(float64(slices.Max(now))/float64(slices.Max(was)), 3)) {
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
