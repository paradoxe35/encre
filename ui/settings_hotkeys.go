package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
)

func (w *MainWindow) createHotkeysSection() fyne.CanvasObject {
	w.captures = make(map[config.ActionKind]*HotkeyCapture, len(config.ActionOrder))
	w.enables = make(map[config.ActionKind]*widget.Check, len(config.ActionOrder))

	rows := make([]fyne.CanvasObject, 0, len(config.ActionOrder)*2)
	captures := make([]*HotkeyCapture, 0, len(config.ActionOrder))

	for _, kind := range config.ActionOrder {
		action := w.config.Action(kind)

		enable := w.dirtyCheck("", action.Enabled)
		w.enables[kind] = enable

		capture := w.newCapture(kind)
		w.captures[kind] = capture
		captures = append(captures, capture)

		label := widget.NewLabel(kind.Label())
		label.TextStyle.Bold = true

		rows = append(rows,
			container.NewBorder(nil, nil, container.NewHBox(enable, label), nil, capture),
			widget.NewSeparator(),
		)
	}

	// Siblings refuse duplicate bindings and keep only one capture recording at a time.
	for _, capture := range captures {
		capture.SetSiblings(others(captures, capture)...)
	}

	help := widget.NewLabel(
		"• Click 'Capture', press the keys in sequence, then Enter to save\n" +
			"• Requires at least one modifier (Ctrl/Alt/Shift/Super)\n" +
			"• Press ESC to cancel")
	help.Wrapping = fyne.TextWrapWord

	reset := widget.NewButton("Reset to defaults", func() {
		defaults := config.DefaultActions()
		for kind, capture := range w.captures {
			capture.StopCapture()
			w.hotkeyBindings[kind].Set(defaults[kind].Hotkey)
			capture.UpdateFromBinding()
		}
		w.markDirty()
	})

	rows = append(rows,
		container.NewPadded(container.NewVBox(
			widget.NewLabel("How to capture hotkeys"),
			help,
		)),
		container.NewHBox(reset),
	)

	return container.NewVScroll(container.NewPadded(container.NewVBox(rows...)))
}

func (w *MainWindow) newCapture(kind config.ActionKind) *HotkeyCapture {
	capture := NewHotkeyCapture(w.hotkeyBindings[kind])
	capture.window = w.Window
	capture.onCaptureStart = w.hotkeyManager.Disable
	capture.onCaptureStop = w.hotkeyManager.Enable
	capture.onChanged = w.markDirty
	return capture
}

func others(all []*HotkeyCapture, self *HotkeyCapture) []*HotkeyCapture {
	rest := make([]*HotkeyCapture, 0, len(all)-1)
	for _, capture := range all {
		if capture != self {
			rest = append(rest, capture)
		}
	}
	return rest
}
