package ui

import (
	"os"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/paradoxe35/encre/internal/input"
)

// Widgets need a driver before they can refresh.
func TestMain(m *testing.M) {
	test.NewApp()
	os.Exit(m.Run())
}

func lister(devices ...input.Device) func() []input.Device {
	return func() []input.Device { return devices }
}

func TestPickerDefaultsToSystemDefault(t *testing.T) {
	p := newMicrophonePicker("", lister(
		input.Device{Name: "Built-in", IsDefault: true},
		input.Device{Name: "Headset"},
	))

	if p.Device() != "" {
		t.Errorf("Device() = %q, want empty for the system default", p.Device())
	}
	if p.selector.Selected != systemDefaultDevice {
		t.Errorf("selected %q, want %q", p.selector.Selected, systemDefaultDevice)
	}
	if p.status.Text != "System default is Built-in" {
		t.Errorf("status = %q", p.status.Text)
	}
}

func TestPickerKeepsSavedDevice(t *testing.T) {
	p := newMicrophonePicker("Headset", lister(
		input.Device{Name: "Built-in", IsDefault: true},
		input.Device{Name: "Headset"},
	))

	if p.Device() != "Headset" {
		t.Errorf("Device() = %q, want Headset", p.Device())
	}
}

func TestPickerKeepsAnUnpluggedDeviceListed(t *testing.T) {
	p := newMicrophonePicker("Headset", lister(
		input.Device{Name: "Built-in", IsDefault: true},
	))

	if p.Device() != "Headset" {
		t.Errorf("Device() = %q, want the saved Headset to survive", p.Device())
	}

	var listed bool
	for _, option := range p.selector.Options {
		if option == "Headset" {
			listed = true
		}
	}
	if !listed {
		t.Error("the saved device should stay in the list while missing")
	}
	if p.status.Text != "Headset is not connected. The default will be used until it returns." {
		t.Errorf("status should explain the fallback, got %q", p.status.Text)
	}
}

func TestPickerWithNoMicrophone(t *testing.T) {
	p := newMicrophonePicker("", lister())

	if p.Device() != "" {
		t.Errorf("Device() = %q, want empty", p.Device())
	}
	if p.status.Text != "No microphone found. Connect one and press Rescan." {
		t.Errorf("status = %q", p.status.Text)
	}
	if len(p.selector.Options) != 1 {
		t.Errorf("only the default option should be offered, got %v", p.selector.Options)
	}
}

func TestPickerDoesNotDuplicateSavedDevice(t *testing.T) {
	p := newMicrophonePicker("Headset", lister(input.Device{Name: "Headset", IsDefault: true}))

	count := 0
	for _, option := range p.selector.Options {
		if option == "Headset" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Headset appears %d times, want once", count)
	}
}
