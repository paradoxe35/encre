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

import "fmt"

// macOS has no public way to set another app's volume, so the output device is turned down instead.
func lower() (func() error, error) {
	var device C.AudioObjectID
	if status := C.encre_default_output(&device); status != 0 {
		return nil, coreAudioError("finding the output device", status)
	}

	var volume C.Float32
	if status := C.encre_get_volume(device, &volume); status != 0 {
		return nil, coreAudioError("reading the output volume", status)
	}
	if status := C.encre_set_volume(device, volume*C.Float32(sliderGain)); status != 0 {
		return nil, coreAudioError("lowering the output volume", status)
	}

	return func() error {
		if status := C.encre_set_volume(device, volume); status != 0 {
			return coreAudioError("restoring the output volume", status)
		}
		return nil
	}, nil
}

func coreAudioError(action string, status C.OSStatus) error {
	return fmt.Errorf("%s: Core Audio status %d", action, int32(status))
}
