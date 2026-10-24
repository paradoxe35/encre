package ducking

/*
#cgo LDFLAGS: -framework CoreAudio -framework AudioToolbox
#include <CoreAudio/CoreAudio.h>
#include <AudioToolbox/AudioServices.h>

static OSStatus encre_default_output(AudioObjectID* device) {
    AudioObjectPropertyAddress address = {
        kAudioHardwarePropertyDefaultOutputDevice, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain,
    };
    UInt32 size = sizeof(*device);
    return AudioObjectGetPropertyData(kAudioObjectSystemObject, &address, 0, NULL, &size, device);
}

static const AudioObjectPropertyAddress encre_volume = {
    kAudioHardwareServiceDeviceProperty_VirtualMainVolume, kAudioDevicePropertyScopeOutput, kAudioObjectPropertyElementMain,
};

static OSStatus encre_get_volume(AudioObjectID device, Float32* volume) {
    UInt32 size = sizeof(*volume);
    return AudioObjectGetPropertyData(device, &encre_volume, 0, NULL, &size, volume);
}

static OSStatus encre_set_volume(AudioObjectID device, Float32 volume) {
    return AudioObjectSetPropertyData(device, &encre_volume, 0, NULL, sizeof(volume), &volume);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"math"
)

// outputSession turns the output device down: macOS has no public way to set another app's volume.
type outputSession struct {
	device C.AudioObjectID
	volume C.Float32
}

func open() (session, error) {
	s := &outputSession{}
	if status := C.encre_default_output(&s.device); status != 0 {
		return nil, coreAudioError("finding the output device", status)
	}
	if status := C.encre_get_volume(s.device, &s.volume); status != 0 {
		return nil, coreAudioError("reading the output volume", status)
	}
	return s, nil
}

// The output volume is a slider, close to cubic, so an amplitude share is its cube root there.
func (s *outputSession) scale(share float64) error {
	if status := C.encre_set_volume(s.device, s.volume*C.Float32(math.Cbrt(share))); status != 0 {
		return coreAudioError("setting the output volume", status)
	}
	return nil
}

type outputLevel struct {
	Device uint32  `json:"device"`
	Volume float32 `json:"volume"`
}

func (s *outputSession) found() any {
	return outputLevel{Device: uint32(s.device), Volume: float32(s.volume)}
}

func (s *outputSession) close() {}

// Only the same output, and only ever raised: a volume turned up since is left as it is.
// While another output is in use, or on failure, the level is handed back to be tried again later.
func restore(data []byte) ([]byte, error) {
	var saved outputLevel
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}

	var device C.AudioObjectID
	if status := C.encre_default_output(&device); status != 0 {
		return data, coreAudioError("finding the output device", status)
	}
	if uint32(device) != saved.Device {
		return data, nil
	}
	var volume C.Float32
	if status := C.encre_get_volume(device, &volume); status != 0 {
		return data, coreAudioError("reading the output volume", status)
	}
	if float32(volume) >= saved.Volume {
		return nil, nil
	}
	if status := C.encre_set_volume(device, C.Float32(saved.Volume)); status != 0 {
		return data, coreAudioError("setting the output volume", status)
	}
	return nil, nil
}

func coreAudioError(action string, status C.OSStatus) error {
	return fmt.Errorf("%s: Core Audio status %d", action, int32(status))
}
