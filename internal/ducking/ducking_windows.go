package ducking

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const sFalse = 1

// mixerSession's COM objects live on the worker thread that opened them.
type mixerSession struct {
	volumes []*wca.ISimpleAudioVolume
	levels  []float32
	saved   []mixerLevel
}

// mixerLevel is keyed by the session identifier, which Windows also keys its remembered app volumes by.
type mixerLevel struct {
	Session string  `json:"session"`
	Level   float32 `json:"level"`
}

func open() (session, error) {
	if err := coInitialize(); err != nil {
		return nil, err
	}

	s := &mixerSession{}
	if err := eachSession(s.add); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func coInitialize() error {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if oleErr, ok := errors.AsType[*ole.OleError](err); !ok || oleErr.Code() != sFalse {
			return err
		}
	}
	return nil
}

// eachSession calls visit with every session on the default output, active or not.
func eachSession(visit func(*wca.IAudioSessionControl, *wca.IAudioSessionControl2)) error {
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
	for i := range count {
		visitSession(sessions, i, visit)
	}
	return nil
}

func visitSession(sessions *wca.IAudioSessionEnumerator, i int, visit func(*wca.IAudioSessionControl, *wca.IAudioSessionControl2)) {
	var control *wca.IAudioSessionControl
	if sessions.GetSession(i, &control) != nil {
		return
	}
	defer control.Release()

	var details *wca.IAudioSessionControl2
	if control.PutQueryInterface(wca.IID_IAudioSessionControl2, &details) != nil {
		return
	}
	defer details.Release()

	// Returns no error exactly when it is the system sounds session, which is not an app's.
	if details.IsSystemSoundsSession() == nil {
		return
	}
	visit(control, details)
}

// add keeps a session only when it is another app's, and playing.
func (s *mixerSession) add(control *wca.IAudioSessionControl, details *wca.IAudioSessionControl2) {
	var pid, state uint32
	if details.GetProcessId(&pid) != nil || pid == uint32(os.Getpid()) {
		return
	}
	if control.GetState(&state) != nil || state != wca.AudioSessionStateActive {
		return
	}

	var volume *wca.ISimpleAudioVolume
	if control.PutQueryInterface(wca.IID_ISimpleAudioVolume, &volume) != nil {
		return
	}
	var level float32
	if volume.GetMasterVolume(&level) != nil {
		volume.Release()
		return
	}
	s.volumes = append(s.volumes, volume)
	s.levels = append(s.levels, level)
	if id, ok := sessionIdentifier(details); ok {
		s.saved = append(s.saved, mixerLevel{Session: id, Level: level})
	}
}

// Called directly: go-wca's wrapper reads the returned string even when the call failed.
func sessionIdentifier(details *wca.IAudioSessionControl2) (string, bool) {
	var id *uint16
	hr, _, _ := syscall.SyscallN(details.VTable().GetSessionIdentifier,
		uintptr(unsafe.Pointer(details)), uintptr(unsafe.Pointer(&id)))
	if hr != 0 || id == nil {
		return "", false
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(id)))
	return ole.LpOleStrToString(id), true
}

// The mixer's levels apply to amplitude, so the share is used as it is.
// A session that refuses once has usually ended, so it is let go rather than retried every step.
func (s *mixerSession) scale(share float64) error {
	var errs []error
	kept := 0
	for i, volume := range s.volumes {
		if err := volume.SetMasterVolume(s.levels[i]*float32(share), nil); err != nil {
			errs = append(errs, err)
			volume.Release()
			continue
		}
		s.volumes[kept], s.levels[kept] = volume, s.levels[i]
		kept++
	}
	clear(s.volumes[kept:])
	s.volumes, s.levels = s.volumes[:kept], s.levels[:kept]
	return errors.Join(errs...)
}

func (s *mixerSession) found() any { return s.saved }

func (s *mixerSession) close() {
	for _, volume := range s.volumes {
		volume.Release()
	}
	ole.CoUninitialize()
}

// Windows remembers each app's volume, so an app restarted since the crash is raised too.
// A session no longer at its ducked level was set since and is left as it is.
// It returns the sessions it could not reach yet, kept for a later try.
func restore(data []byte) ([]byte, error) {
	var saved []mixerLevel
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return nil, nil
	}
	levels := map[string]float32{}
	for _, level := range saved {
		levels[level.Session] = max(levels[level.Session], level.Level)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := coInitialize(); err != nil {
		return data, err
	}
	defer ole.CoUninitialize()

	reached := map[string]bool{}
	var errs []error
	err := eachSession(func(control *wca.IAudioSessionControl, details *wca.IAudioSessionControl2) {
		id, ok := sessionIdentifier(details)
		if !ok {
			return
		}
		want, ok := levels[id]
		if !ok {
			return
		}
		var volume *wca.ISimpleAudioVolume
		if control.PutQueryInterface(wca.IID_ISimpleAudioVolume, &volume) != nil {
			return
		}
		defer volume.Release()

		var level float32
		if volume.GetMasterVolume(&level) != nil {
			return
		}
		if stillDucked(float64(level) / float64(want)) {
			if err := volume.SetMasterVolume(want, nil); err != nil {
				errs = append(errs, err)
				return
			}
		}
		reached[id] = true
	})
	if err != nil {
		return data, errors.Join(append(errs, err)...)
	}

	var left []mixerLevel
	for _, level := range saved {
		if !reached[level.Session] {
			left = append(left, level)
		}
	}
	rest, marshalErr := leftOver(left)
	return rest, errors.Join(append(errs, marshalErr)...)
}
