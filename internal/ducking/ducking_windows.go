package ducking

import (
	"errors"
	"os"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const sFalse = 1

// mixerSession's COM objects live on the worker thread that opened them.
type mixerSession struct {
	volumes []*wca.ISimpleAudioVolume
	levels  []float32
}

func open() (session, error) {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if oleErr, ok := errors.AsType[*ole.OleError](err); !ok || oleErr.Code() != sFalse {
			return nil, err
		}
	}

	s := &mixerSession{}
	if err := s.find(); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *mixerSession) find() error {
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
		s.add(sessions, i)
	}
	return nil
}

// add keeps a session only when it is another app's, and playing.
func (s *mixerSession) add(sessions *wca.IAudioSessionEnumerator, i int) {
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
}

// The mixer's levels apply to amplitude, so the share is used as it is.
func (s *mixerSession) scale(share float64) error {
	var errs []error
	for i, volume := range s.volumes {
		errs = append(errs, volume.SetMasterVolume(s.levels[i]*float32(share), nil))
	}
	return errors.Join(errs...)
}

func (s *mixerSession) close() {
	for _, volume := range s.volumes {
		volume.Release()
	}
	ole.CoUninitialize()
}
