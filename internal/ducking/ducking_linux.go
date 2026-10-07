package ducking

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

const requestTimeout = time.Second

// Each app's stream is turned down through the sound server, which PipeWire serves too, so any player
// is reached whether or not it answers to remote control.
func lower() (func() error, error) {
	client, err := connect()
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var streams proto.GetSinkInputInfoListReply
	if err := client.RawRequest(&proto.GetSinkInputInfoList{}, &streams); err != nil {
		return nil, err
	}

	own := strconv.Itoa(os.Getpid())
	saved := map[uint32]proto.ChannelVolumes{}
	for _, stream := range streams {
		if stream.Corked || !stream.VolumeWritable || stream.Properties["application.process.id"].String() == own {
			continue
		}
		if err := setVolume(client, stream.SinkInputIndex, scaled(stream.ChannelVolumes, sliderGain)); err == nil {
			saved[stream.SinkInputIndex] = stream.ChannelVolumes
		}
	}

	return func() error { return restore(saved) }, nil
}

// A stream that ended meanwhile has nothing to restore, so only the others' errors count.
func restore(saved map[uint32]proto.ChannelVolumes) error {
	if len(saved) == 0 {
		return nil
	}
	client, err := connect()
	if err != nil {
		return err
	}
	defer client.Close()

	var errs []error
	for index, volumes := range saved {
		if err := setVolume(client, index, volumes); err != nil && !errors.Is(err, proto.ErrNoSuchEntity) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func connect() (*pulse.Client, error) {
	return pulse.NewClient(pulse.ClientApplicationName("Encre"), pulse.ClientTimeout(requestTimeout))
}

func setVolume(client *pulse.Client, index uint32, volumes proto.ChannelVolumes) error {
	return client.RawRequest(&proto.SetSinkInputVolume{SinkInputIndex: index, ChannelVolumes: volumes}, nil)
}

func scaled(volumes proto.ChannelVolumes, by float64) proto.ChannelVolumes {
	quieter := make(proto.ChannelVolumes, len(volumes))
	for i, volume := range volumes {
		quieter[i] = proto.Volume(float64(volume) * by)
	}
	return quieter
}
