package ui

import (
	"fmt"
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
	stopBtn        *captureSaveButton
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
	// savePressed lets the focus loss from pressing Save through, so the tap that follows can save.
	savePressed bool
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

// A key pressed with Ctrl, Alt or Super arrives here instead of TypedKey, and must never reach Entry.
func (e *captureEntry) TypedShortcut(shortcut fyne.Shortcut) {
	if modifiers, key, ok := shortcutCombo(shortcut); ok {
		e.parent.recordCombo(modifiers, key)
	}
}

func (e *captureEntry) FocusLost() {
	e.Entry.FocusLost()
	e.parent.focusLost()
}

func shortcutCombo(shortcut fyne.Shortcut) (fyne.KeyModifier, fyne.KeyName, bool) {
	primary := fyne.KeyModifierShortcutDefault
	switch s := shortcut.(type) {
	case *desktop.CustomShortcut:
		return s.Modifier, s.KeyName, true
	case *fyne.ShortcutPaste:
		if s.Secondary {
			return fyne.KeyModifierShift, fyne.KeyInsert, true
		}
		return primary, fyne.KeyV, true
	case *fyne.ShortcutCopy:
		if s.Secondary {
			return primary, fyne.KeyInsert, true
		}
		return primary, fyne.KeyC, true
	case *fyne.ShortcutCut:
		if s.Secondary {
			return fyne.KeyModifierShift, fyne.KeyDelete, true
		}
		return primary, fyne.KeyX, true
	case *fyne.ShortcutSelectAll:
		return primary, fyne.KeyA, true
	case *fyne.ShortcutUndo:
		return primary, fyne.KeyZ, true
	case *fyne.ShortcutRedo:
		return primary, fyne.KeyY, true
	}
	return 0, "", false
}

type captureSaveButton struct {
	widget.Button
	parent *HotkeyCapture
	held   bool
}

func newCaptureSaveButton(parent *HotkeyCapture) *captureSaveButton {
	b := &captureSaveButton{parent: parent}
	b.Text = "Save"
	b.Icon = theme.ConfirmIcon()
	b.OnTapped = parent.saveAndStop
	b.ExtendBaseWidget(b)
	return b
}

// The press unfocuses the entry before the tap, which would otherwise end the capture unsaved.
func (b *captureSaveButton) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	b.held = true
	b.parent.setSavePressed(true)
}

// Letting go on Save after leaving it while held unfocuses the entry again before the tap.
func (b *captureSaveButton) MouseUp(e *desktop.MouseEvent) {
	if b.held && e.Button == desktop.MouseButtonPrimary {
		b.parent.setSavePressed(true)
	}
	b.held = false
}

func (b *captureSaveButton) MouseIn(e *desktop.MouseEvent) {
	b.Button.MouseIn(e)
	b.held = b.held && e.Button&desktop.MouseButtonPrimary != 0
}

// A press that leaves Save ends in no tap, so the capture takes back the focus it lost.
func (b *captureSaveButton) MouseOut() {
	b.Button.MouseOut()
	if b.held {
		b.parent.setSavePressed(false)
		b.parent.refocus()
	}
}

func (b *captureSaveButton) Tapped(e *fyne.PointEvent) {
	b.held = false
	b.Button.Tapped(e)
	b.parent.setSavePressed(false)
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
		return theme.ColorForWidget(theme.ColorNameDisabledButton, r.entry)
	}
	return theme.ColorForWidget(theme.ColorNameInputBackground, r.entry)
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
	h.entry.PlaceHolder = "Press keys in sequence (Esc to cancel, Enter to save)"
	h.entry.TextStyle.Bold = true
	h.entry.TextStyle.Monospace = true
	h.entry.Hide()

	h.captureBtn = widget.NewButtonWithIcon("Capture", theme.MediaRecordIcon(), func() {
		h.startCapture()
	})

	h.stopBtn = newCaptureSaveButton(h)
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
	h.savePressed = false
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
		if h.window != nil {
			h.window.Canvas().Focus(h.entry)
		}
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
	h.savePressed = false
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

func (h *HotkeyCapture) setSavePressed(pressed bool) {
	h.mu.Lock()
	h.savePressed = pressed
	h.mu.Unlock()
}

func (h *HotkeyCapture) refocus() {
	h.mu.Lock()
	capturing := h.isCapturing
	h.mu.Unlock()
	if capturing && h.window != nil {
		h.window.Canvas().Focus(h.entry)
	}
}

func (h *HotkeyCapture) focusLost() {
	h.mu.Lock()
	pressed := h.savePressed
	h.savePressed = false
	h.mu.Unlock()
	if !pressed {
		h.stopCapture()
	}
}

func (h *HotkeyCapture) recordCombo(modifiers fyne.KeyModifier, key fyne.KeyName) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.isCapturing {
		return
	}
	for _, modifier := range []fyne.KeyModifier{fyne.KeyModifierShift, fyne.KeyModifierControl, fyne.KeyModifierAlt, fyne.KeyModifierSuper} {
		if modifiers&modifier != 0 {
			h.modifiers[modifier] = true
		}
	}
	if !isModifierKey(key) && registrable(key) {
		h.pressedKeys = map[fyne.KeyName]bool{key: true}
	}
	h.updateDisplay()
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
		if !isModifierKey(key.Name) && registrable(key.Name) {
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

// The keys the hotkey listener knows by name; others, such as ö or numpad keys, would be saved as
// a name it cannot read, or as nothing at all.
var registrableKeys = func() map[string]bool {
	keys := map[string]bool{
		"space": true, "return": true, "escape": true, "tab": true, "backspace": true, "delete": true,
		"insert": true, "home": true, "end": true, "pageup": true, "pagedown": true,
		"up": true, "down": true, "left": true, "right": true,
	}
	for c := 'a'; c <= 'z'; c++ {
		keys[string(c)] = true
	}
	for c := '0'; c <= '9'; c++ {
		keys[string(c)] = true
	}
	for i := 1; i <= 12; i++ {
		keys[fmt.Sprintf("f%d", i)] = true
	}
	return keys
}()

func registrable(key fyne.KeyName) bool { return registrableKeys[keyNameToString(key)] }

func keyNameToString(key fyne.KeyName) string {
	keyStr := strings.ToLower(string(key))

	switch key {
	case fyne.KeyPageUp:
		return "pageup"
	case fyne.KeyPageDown:
		return "pagedown"
	case fyne.KeySpace:
		return "space"
	case fyne.KeyEscape:
		return "escape"
	case fyne.KeyReturn, fyne.KeyEnter:
		return "return"
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
