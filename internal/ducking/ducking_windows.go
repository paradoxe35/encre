package ducking

import (
	"errors"
	"os"
	"runtime"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const sFalse = 1

// Each app's session on the default output is turned down, as its slider in the volume mixer would be.
// Sessions are found again to restore them, since the COM objects belong to the thread that made them.
func lower() (func() error, error) {
	saved := map[string]float32{}
	err := eachSession(func(id string, active bool, volume *wca.ISimpleAudioVolume) error {
		if !active {
			return nil
		}
		var level float32
		if err := volume.GetMasterVolume(&level); err != nil {
			return err
		}
		if err := volume.SetMasterVolume(level*float32(gain), nil); err != nil {
			return err
		}
		saved[id] = level
		return nil
	})
	if len(saved) == 0 && err != nil {
		return nil, err
	}

	return func() error {
		return eachSession(func(id string, _ bool, volume *wca.ISimpleAudioVolume) error {
			if level, ok := saved[id]; ok {
				return volume.SetMasterVolume(level, nil)
			}
			return nil
		})
	}, nil
}

// eachSession visits the sessions of other apps playing to the default output.
func eachSession(visit func(id string, active bool, volume *wca.ISimpleAudioVolume) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if oleErr, ok := errors.AsType[*ole.OleError](err); !ok || oleErr.Code() != sFalse {
			return err
		}
	}
	defer ole.CoUninitialize()

	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return err
	}
	defer enumerator.Release()

	var device *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &device); err != nil {
		return err
	}
	defer device.Release()

	var manager *wca.IAudioSessionManager2
	if err := device.Activate(wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &manager); err != nil {
		return err
	}
	defer manager.Release()

	var sessions *wca.IAudioSessionEnumerator
	if err := manager.GetSessionEnumerator(&sessions); err != nil {
		return err
	}
	defer sessions.Release()

	var count int
	if err := sessions.GetCount(&count); err != nil {
		return err
	}

	var errs []error
	for i := range count {
		errs = append(errs, visitSession(sessions, i, visit))
	}
	return errors.Join(errs...)
}

func visitSession(sessions *wca.IAudioSessionEnumerator, i int, visit func(string, bool, *wca.ISimpleAudioVolume) error) error {
	var control *wca.IAudioSessionControl
	if err := sessions.GetSession(i, &control); err != nil {
		return err
	}
	defer control.Release()

	var details *wca.IAudioSessionControl2
	if err := control.PutQueryInterface(wca.IID_IAudioSessionControl2, &details); err != nil {
		return err
	}
	defer details.Release()

	// Returns no error exactly when it is the system sounds session, which is not an app's.
	if details.IsSystemSoundsSession() == nil {
		return nil
	}
	var pid uint32
	if err := details.GetProcessId(&pid); err != nil || pid == uint32(os.Getpid()) {
		return nil
	}

	var id string
	if err := details.GetSessionInstanceIdentifier(&id); err != nil {
		return err
	}
	var state uint32
	if err := control.GetState(&state); err != nil {
		return err
	}

	var volume *wca.ISimpleAudioVolume
	if err := control.PutQueryInterface(wca.IID_ISimpleAudioVolume, &volume); err != nil {
		return err
	}
	defer volume.Release()
	return visit(id, state == wca.AudioSessionStateActive, volume)
}
