package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/input"
)

const systemDefaultDevice = "System default"

// MicrophonePicker keeps a saved device listed and selected even while it is unplugged.
type MicrophonePicker struct {
	widget.BaseWidget

	selector   *widget.Select
	status     *widget.Label
	saved      string
	chosen     string
	refreshing bool
	onChanged  func()

	// Injected so the empty and unplugged cases can be tested without hardware.
	list func() []input.Device
}

func NewMicrophonePicker(saved string) *MicrophonePicker {
	return newMicrophonePicker(saved, input.InputDevices)
}

func newMicrophonePicker(saved string, list func() []input.Device) *MicrophonePicker {
	p := &MicrophonePicker{}
	p.selector = widget.NewSelect(nil, func(string) {
		if p.refreshing {
			return
		}
		p.chosen = p.selector.Selected
		if p.onChanged != nil {
			p.onChanged()
		}
	})
	*p = MicrophonePicker{
		selector: p.selector,
		status:   widget.NewLabel(""),
		saved:    saved,
		chosen:   saved,
		list:     list,
	}
	p.status.TextStyle.Italic = true

	p.ExtendBaseWidget(p)
	p.Refresh()
	return p
}

func (p *MicrophonePicker) Device() string {
	if p.chosen == systemDefaultDevice {
		return ""
	}
	return p.chosen
}

func (p *MicrophonePicker) Refresh() {
	p.refreshing = true
	defer func() { p.refreshing = false }()

	devices := p.list()

	wanted := p.chosen
	if _, ok := p.find(devices, wanted); !ok {
		wanted = p.saved
	}
	p.chosen = wanted

	options := []string{systemDefaultDevice}
	present := false
	for _, device := range devices {
		options = append(options, device.Name)
		if device.Name == p.saved {
			present = true
		}
	}
	// Keep a missing device visible rather than dropping the user's choice.
	if p.chosen != "" && !p.inOptions(options, p.chosen) {
		options = append(options, p.chosen)
	} else if p.saved != "" && !present && !p.inOptions(options, p.saved) {
		options = append(options, p.saved)
	}

	p.selector.Options = options
	p.selector.SetSelected(p.selection())
	p.describe(devices, present)

	p.BaseWidget.Refresh()
}

func (p *MicrophonePicker) find(devices []input.Device, name string) (input.Device, bool) {
	if name == "" {
		return input.Device{}, true
	}
	for _, device := range devices {
		if device.Name == name {
			return device, true
		}
	}
	return input.Device{}, false
}

func (p *MicrophonePicker) inOptions(options []string, name string) bool {
	for _, option := range options {
		if option == name {
			return true
		}
	}
	return false
}

func (p *MicrophonePicker) selection() string {
	if p.chosen == "" {
		return systemDefaultDevice
	}
	return p.chosen
}

func (p *MicrophonePicker) describe(devices []input.Device, present bool) {
	switch {
	case len(devices) == 0:
		p.status.SetText("No microphone found. Connect one and press Rescan.")
	case p.saved != "" && !present:
		p.status.SetText(p.saved + " is not connected. The default will be used until it returns.")
	default:
		p.status.SetText(defaultName(devices))
	}
}

func defaultName(devices []input.Device) string {
	for _, device := range devices {
		if device.IsDefault {
			return "System default is " + device.Name
		}
	}
	return ""
}

func (p *MicrophonePicker) CreateRenderer() fyne.WidgetRenderer {
	rescan := widget.NewButton("Rescan", func() {
		p.saved = p.Device()
		p.Refresh()
	})

	return widget.NewSimpleRenderer(container.NewVBox(
		container.NewBorder(nil, nil, nil, rescan, p.selector),
		p.status,
	))
}
