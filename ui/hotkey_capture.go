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

// HotkeyCapture is a custom widget for capturing keyboard shortcuts using Fyne's keyboard events
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

	pressedKeys       map[fyne.KeyName]bool
	modifiers         map[fyne.KeyModifier]bool
	allowModifierOnly bool
}

// captureEntry is a custom entry widget that captures keyboard events with white background
type captureEntry struct {
	widget.Entry
	parent *HotkeyCapture
}

func (e *captureEntry) TypedKey(key *fyne.KeyEvent) {
	if e.parent != nil {
		e.parent.handleKeyPress(key)
	}
}

// TypedRune prevents normal text input during capture, so pressing F doesn't type "f".
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

func NewHotkeyCapture(binding binding.String, placeholder string) *HotkeyCapture {
	h := &HotkeyCapture{
		binding:     binding,
		pressedKeys: make(map[fyne.KeyName]bool),
		modifiers:   make(map[fyne.KeyModifier]bool),
	}

	h.displayLabel = widget.NewLabel(placeholder)
	h.displayLabel.TextStyle.Bold = true
	h.displayLabel.TextStyle.Monospace = true

	currentValue, _ := binding.Get()
	if currentValue != "" {
		h.displayLabel.SetText(currentValue)
	}

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
	h.syncClearButton()

	// Stack so only the label or the entry is visible at a time.
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

	// Global hotkeys must be off while capturing, or they'd fire on the keys being recorded.
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
	parts := []string{}

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

	if len(h.pressedKeys) > 0 {
		keyNames := make([]string, 0, len(h.pressedKeys))
		for keyName := range h.pressedKeys {
			keyNames = append(keyNames, keyNameToString(keyName))
		}
		parts = append(parts, keyNames...)
	}

	if len(parts) > 0 {
		h.entry.SetText(strings.Join(parts, "+"))
	} else {
		h.entry.SetText("")
	}
}

// saveHotkey returns true if the combination is valid and was saved.
func (h *HotkeyCapture) saveHotkey() bool {
	parts := []string{}

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

	keyNames := make([]string, 0, len(h.pressedKeys))
	for keyName := range h.pressedKeys {
		keyNames = append(keyNames, keyNameToString(keyName))
	}
	parts = append(parts, keyNames...)

	// Requires at least one modifier, plus either a key or (if allowed) a modifier-only combo like Ctrl+Super.
	valid := false
	if len(h.modifiers) > 0 {
		if len(h.pressedKeys) > 0 {
			valid = true
		} else if h.allowModifierOnly && isModifierOnlyAllowed(h.modifiers) {
			valid = true
		}
	}
	if !valid {
		h.entry.SetText("Invalid combination (need modifier + key)")
		return false
	}

	hotkeyStr := strings.Join(parts, "+")

	for _, sibling := range h.siblings {
		if sibling == h {
			continue
		}
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

// The clear button only earns its place next to a hotkey that exists.
func (h *HotkeyCapture) syncClearButton() {
	if value, _ := h.binding.Get(); value != "" {
		h.clearBtn.Show()
	} else {
		h.clearBtn.Hide()
	}
}

// StopCapture stops capture if currently capturing.
func (h *HotkeyCapture) StopCapture() {
	h.mu.Lock()
	isCapturing := h.isCapturing
	h.mu.Unlock()

	if isCapturing {
		h.stopCapture()
	}
}

// UpdateFromBinding refreshes the label and the clear button from the binding's current value.
func (h *HotkeyCapture) UpdateFromBinding() {
	currentValue, _ := h.binding.Get()
	if currentValue != "" {
		h.displayLabel.SetText(currentValue)
	} else {
		h.displayLabel.SetText(unsetHotkeyText)
	}
	h.syncClearButton()
}

// SetSiblings sets other capture widgets that should be disabled during capture
func (h *HotkeyCapture) SetSiblings(siblings ...*HotkeyCapture) {
	h.siblings = siblings
}

// SetAllowModifierOnly allows accepting modifier-only combinations (e.g., Ctrl+Win)
func (h *HotkeyCapture) SetAllowModifierOnly(allow bool) {
	h.allowModifierOnly = allow
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
		return "escape" // Match FFI expectation
	case fyne.KeyReturn, fyne.KeyEnter:
		return "return" // Match FFI expectation
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
		if len(keyStr) == 1 {
			return keyStr
		}
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
