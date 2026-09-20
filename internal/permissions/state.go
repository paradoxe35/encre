package permissions

type Type string

const (
	Accessibility   Type = "accessibility"
	InputMonitoring Type = "input_monitoring"
	Microphone      Type = "microphone"
)

func (t Type) DisplayName() string {
	switch t {
	case Accessibility:
		return "Accessibility"
	case InputMonitoring:
		return "Input Monitoring"
	case Microphone:
		return "Microphone"
	default:
		return string(t)
	}
}

// The microphone only counts once refused: while undecided, the system asks by itself the
// first time dictation records.
type State struct {
	AccessibilityGranted   bool
	InputMonitoringGranted bool
	MicrophoneDenied       bool
}

func (s State) AllGranted() bool {
	return s.AccessibilityGranted && s.InputMonitoringGranted && !s.MicrophoneDenied
}

// Hotkey permissions only take effect after a relaunch; the microphone does not.
func (s State) NeedsRestart() bool {
	return !s.AccessibilityGranted || !s.InputMonitoringGranted
}

func (s State) Granted(t Type) bool {
	switch t {
	case Accessibility:
		return s.AccessibilityGranted
	case InputMonitoring:
		return s.InputMonitoringGranted
	case Microphone:
		return !s.MicrophoneDenied
	default:
		return false
	}
}

func (s State) Missing() []Type {
	missing := make([]Type, 0, 3)
	for _, t := range []Type{Accessibility, InputMonitoring, Microphone} {
		if !s.Granted(t) {
			missing = append(missing, t)
		}
	}
	return missing
}
