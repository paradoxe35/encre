package permissions

// Type identifies a macOS permission the application depends on.
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

// State is what macOS currently allows. The microphone is only a problem once
// refused: while undecided, the system asks by itself the first time dictation
// records, so the card stays out of the way.
type State struct {
	AccessibilityGranted   bool
	InputMonitoringGranted bool
	MicrophoneDenied       bool
}

func (s State) AllGranted() bool {
	return s.AccessibilityGranted && s.InputMonitoringGranted && !s.MicrophoneDenied
}

// NeedsRestart reports whether a grant only takes effect after a relaunch,
// which is true of the hotkey permissions and not of the microphone.
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
