package ui

import (
	"image/color"
	"runtime"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type HotkeyCapture struct {
	widget.BaseWidget
	binding        binding.String
	captureBtn     *widget.Button
	stopBtn        *widget.Button
	clearBtn       *widget.Button
	container      *fyne.Container
	displayLabel   *widget.Label
	entry          *captureEntry
	window         fyne.Window
	onCaptureStart func()
	onCaptureStop  func()
	onChanged      func()
	siblings       []*HotkeyCapture

	isCapturing bool
	mu          sync.Mutex

	pressedKeys map[fyne.KeyName]bool
	modifiers   map[fyne.KeyModifier]bool
}

type captureEntry struct {
	widget.Entry
	parent *HotkeyCapture
}

func (e *captureEntry) TypedKey(key *fyne.KeyEvent) {
	e.parent.handleKeyPress(key)
}

// Swallows runes so pressing F during capture does not type "f".
func (e *captureEntry) TypedRune(r rune) {
}

func (e *captureEntry) CreateRenderer() fyne.WidgetRenderer {
	e.ExtendBaseWidget(e)
	renderer := e.Entry.CreateRenderer()

	return &themedBackgroundRenderer{
		WidgetRenderer: renderer,
		entry:          e,
	}
}

type themedBackgroundRenderer struct {
	fyne.WidgetRenderer
	entry *captureEntry
}

func (r *themedBackgroundRenderer) BackgroundColor() color.Color {
	if r.entry.Disabled() {
		return theme.Color(theme.ColorNameDisabledButton)
	}
	return theme.Color(theme.ColorNameInputBackground)
}

const unsetHotkeyText = "Click 'Capture' to set"

func NewHotkeyCapture(binding binding.String) *HotkeyCapture {
	h := &HotkeyCapture{
		binding:     binding,
		pressedKeys: make(map[fyne.KeyName]bool),
		modifiers:   make(map[fyne.KeyModifier]bool),
	}

	h.displayLabel = widget.NewLabel("")
	h.displayLabel.TextStyle.Bold = true
	h.displayLabel.TextStyle.Monospace = true

	h.entry = &captureEntry{parent: h}
	h.entry.PlaceHolder = "Press keys in sequence (ESC to cancel, Enter to save)"
	h.entry.TextStyle.Bold = true
	h.entry.TextStyle.Monospace = true
	h.entry.Hide()

	h.captureBtn = widget.NewButtonWithIcon("Capture", theme.MediaRecordIcon(), func() {
		h.startCapture()
	})

	h.stopBtn = widget.NewButtonWithIcon("Save", theme.ConfirmIcon(), func() {
		h.saveAndStop()
	})
	h.stopBtn.Importance = widget.HighImportance
	h.stopBtn.Hide()

	h.clearBtn = widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() {
		h.clearHotkey()
	})
	h.clearBtn.Importance = widget.LowImportance
	h.clearBtn.Hide()

	buttonContainer := container.NewHBox(h.captureBtn, h.stopBtn, h.clearBtn)
	h.UpdateFromBinding()

	displayStack := container.NewStack(h.displayLabel, h.entry)

	h.container = container.NewBorder(
		nil, nil,
		nil,
		buttonContainer,
		displayStack,
	)

	h.ExtendBaseWidget(h)
	return h
}

func (h *HotkeyCapture) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(h.container)
}

func (h *HotkeyCapture) startCapture() {
	h.mu.Lock()
	if h.isCapturing {
		h.mu.Unlock()
		return
	}
	h.isCapturing = true
	h.mu.Unlock()

	for _, sibling := range h.siblings {
		sibling.captureBtn.Disable()
	}

	// Global hotkeys must be off while capturing, or they fire on the keys being recorded.
	if h.onCaptureStart != nil {
		h.onCaptureStart()
	}

	h.pressedKeys = make(map[fyne.KeyName]bool)
	h.modifiers = make(map[fyne.KeyModifier]bool)

	h.displayLabel.Hide()
	h.entry.SetText("")
	h.entry.Show()
	h.captureBtn.Hide()
	h.clearBtn.Hide()
	h.stopBtn.Show()

	if h.window != nil {
		h.window.Canvas().Focus(h.entry)
	}
}

func (h *HotkeyCapture) saveAndStop() {
	if !h.saveHotkey() {
		// Stopping here would hide saveHotkey's error behind the restored old binding.
		return
	}
	h.stopCapture()
}

func (h *HotkeyCapture) stopCapture() {
	h.mu.Lock()
	if !h.isCapturing {
		h.mu.Unlock()
		return
	}

	h.isCapturing = false
	h.mu.Unlock()

	for _, sibling := range h.siblings {
		sibling.captureBtn.Enable()
	}

	if h.onCaptureStop != nil {
		h.onCaptureStop()
	}

	h.entry.Hide()
	h.stopBtn.Hide()
	h.captureBtn.Show()
	h.UpdateFromBinding()
	h.displayLabel.Show()
}

func (h *HotkeyCapture) handleKeyPress(key *fyne.KeyEvent) {
	h.mu.Lock()

	if !h.isCapturing {
		h.mu.Unlock()
		return
	}

	if key.Name == fyne.KeyEscape {
		h.mu.Unlock()
		h.stopCapture()
		return
	}

	if key.Name == fyne.KeyReturn || key.Name == fyne.KeyEnter {
		h.mu.Unlock()
		h.saveAndStop()
		return
	}

	switch key.Name {
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		h.modifiers[fyne.KeyModifierShift] = true
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		h.modifiers[fyne.KeyModifierControl] = true
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		h.modifiers[fyne.KeyModifierAlt] = true
	case desktop.KeySuperLeft, desktop.KeySuperRight:
		h.modifiers[fyne.KeyModifierSuper] = true
	default:
		// Only one non-modifier key is kept; a saved combo only ever matches modifiers plus one key.
		if !isModifierKey(key.Name) {
			h.pressedKeys = map[fyne.KeyName]bool{key.Name: true}
		}
	}

	h.updateDisplay()
	h.mu.Unlock()
}

func (h *HotkeyCapture) updateDisplay() {
	h.entry.SetText(h.combo())
}

// Modifiers first in a fixed order, then the key, in the names the FFI hotkey parser expects.
func (h *HotkeyCapture) combo() string {
	var parts []string
	if h.modifiers[fyne.KeyModifierControl] {
		parts = append(parts, "ctrl")
	}
	if h.modifiers[fyne.KeyModifierAlt] {
		parts = append(parts, getAltName())
	}
	if h.modifiers[fyne.KeyModifierShift] {
		parts = append(parts, "shift")
	}
	if h.modifiers[fyne.KeyModifierSuper] {
		parts = append(parts, getSuperName())
	}
	for keyName := range h.pressedKeys {
		parts = append(parts, keyNameToString(keyName))
	}
	return strings.Join(parts, "+")
}

func (h *HotkeyCapture) saveHotkey() bool {
	valid := len(h.modifiers) > 0 && (len(h.pressedKeys) > 0 || isModifierOnlyAllowed(h.modifiers))
	if !valid {
		h.entry.SetText("Invalid combination (need modifier + key)")
		return false
	}

	hotkeyStr := h.combo()

	for _, sibling := range h.siblings {
		siblingValue, _ := sibling.binding.Get()
		if siblingValue != "" && siblingValue == hotkeyStr {
			h.entry.SetText("Duplicate: '" + hotkeyStr + "' is already used")
			return false
		}
	}

	h.binding.Set(hotkeyStr)
	if h.onChanged != nil {
		h.onChanged()
	}
	return true
}

func (h *HotkeyCapture) clearHotkey() {
	h.binding.Set("")
	if h.onChanged != nil {
		h.onChanged()
	}
	h.UpdateFromBinding()
}

func (h *HotkeyCapture) syncClearButton() {
	if value, _ := h.binding.Get(); value != "" {
		h.clearBtn.Show()
	} else {
		h.clearBtn.Hide()
	}
}

func (h *HotkeyCapture) StopCapture() {
	h.stopCapture()
}

func (h *HotkeyCapture) UpdateFromBinding() {
	currentValue, _ := h.binding.Get()
	if currentValue != "" {
		h.displayLabel.SetText(currentValue)
	} else {
		h.displayLabel.SetText(unsetHotkeyText)
	}
	h.syncClearButton()
}

func (h *HotkeyCapture) SetSiblings(siblings ...*HotkeyCapture) {
	h.siblings = siblings
}

func isModifierKey(key fyne.KeyName) bool {
	return key == desktop.KeyShiftLeft || key == desktop.KeyShiftRight ||
		key == desktop.KeyControlLeft || key == desktop.KeyControlRight ||
		key == desktop.KeyAltLeft || key == desktop.KeyAltRight ||
		key == desktop.KeySuperLeft || key == desktop.KeySuperRight
}

func isModifierOnlyAllowed(mods map[fyne.KeyModifier]bool) bool {
	return mods[fyne.KeyModifierControl] && mods[fyne.KeyModifierSuper]
}

func keyNameToString(key fyne.KeyName) string {
	keyStr := strings.ToLower(string(key))

	switch key {
	case fyne.KeySpace:
		return "space"
	case fyne.KeyEscape:
		return "escape" // name the FFI hotkey parser expects
	case fyne.KeyReturn, fyne.KeyEnter:
		return "return" // name the FFI hotkey parser expects
	case fyne.KeyTab:
		return "tab"
	case fyne.KeyBackspace:
		return "backspace"
	case fyne.KeyDelete:
		return "delete"
	case fyne.KeyUp:
		return "up"
	case fyne.KeyDown:
		return "down"
	case fyne.KeyLeft:
		return "left"
	case fyne.KeyRight:
		return "right"
	default:
		return keyStr
	}
}

func getAltName() string {
	switch runtime.GOOS {
	case "darwin":
		return "option"
	default:
		return "alt"
	}
}

func getSuperName() string {
	switch runtime.GOOS {
	case "darwin":
		return "cmd"
	case "windows":
		return "win"
	default:
		return "super"
	}
}
