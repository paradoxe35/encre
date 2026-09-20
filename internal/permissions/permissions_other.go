//go:build !darwin

package permissions

func IsSupported() bool {
	return false
}

func CurrentState() State {
	return State{AccessibilityGranted: true, InputMonitoringGranted: true}
}

func OpenPreference(t Type) {}
