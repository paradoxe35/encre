package ui

import (
	"image/color"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
)

type hotkeyGate struct{ disabled bool }

// The test theme has no bold monospace face, which the capture field uses.
func withDefaultTheme(t *testing.T) fyne.App {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(theme.DefaultTheme())
	return a
}

func capturing(t *testing.T) (*HotkeyCapture, *hotkeyGate, fyne.Window) {
	t.Helper()
	withDefaultTheme(t)
	value := binding.NewString()
	h := NewHotkeyCapture(value)
	gate := &hotkeyGate{}
	h.onCaptureStart = func() { gate.disabled = true }
	h.onCaptureStop = func() { gate.disabled = false }

	w := test.NewWindow(h)
	t.Cleanup(w.Close)
	h.window = w

	h.startCapture()
	if !gate.disabled || w.Canvas().Focused() != h.entry {
		t.Fatal("test setup: the capture did not start")
	}
	return h, gate, w
}

func primaryName() string {
	if fyne.KeyModifierShortcutDefault == fyne.KeyModifierSuper {
		return getSuperName()
	}
	return "ctrl"
}

func TestStandardShortcutsAreRecordedNotRun(t *testing.T) {
	cases := []struct {
		shortcut fyne.Shortcut
		want     string
	}{
		{&fyne.ShortcutPaste{}, primaryName() + "+v"},
		{&fyne.ShortcutCopy{}, primaryName() + "+c"},
		{&fyne.ShortcutCut{}, primaryName() + "+x"},
		{&fyne.ShortcutSelectAll{}, primaryName() + "+a"},
		{&fyne.ShortcutUndo{}, primaryName() + "+z"},
		{&fyne.ShortcutRedo{}, primaryName() + "+y"},
		{&fyne.ShortcutPaste{Secondary: true}, "shift+insert"},
		{&fyne.ShortcutCut{Secondary: true}, "shift+delete"},
		{&fyne.ShortcutCopy{Secondary: true}, primaryName() + "+insert"},
	}
	for _, tc := range cases {
		t.Run(tc.shortcut.ShortcutName(), func(t *testing.T) {
			h, _, w := capturing(t)
			w.Clipboard().SetContent("pasted text")

			h.entry.TypedShortcut(tc.shortcut)

			if h.entry.Text != tc.want {
				t.Fatalf("capture shows %q, want %q", h.entry.Text, tc.want)
			}
			if !h.saveHotkey() {
				t.Fatal("the recorded combo was refused")
			}
			if got, _ := h.binding.Get(); got != tc.want {
				t.Fatalf("saved %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAComboPressedAtOnceKeepsItsKey(t *testing.T) {
	h, _, _ := capturing(t)

	h.entry.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift})

	if h.entry.Text != "ctrl+shift+r" {
		t.Fatalf("capture shows %q", h.entry.Text)
	}
}

func TestLosingFocusEndsTheCaptureAndRestoresHotkeys(t *testing.T) {
	h, gate, w := capturing(t)

	w.Canvas().Unfocus()

	if h.isCapturing || gate.disabled {
		t.Fatal("an abandoned capture left the global hotkeys off")
	}
}

// Pressing Save takes the focus from the entry before the tap arrives.
func TestSaveStillSavesAfterThePressTakesTheFocus(t *testing.T) {
	h, gate, w := capturing(t)
	h.entry.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyK, Modifier: fyne.KeyModifierAlt})

	h.stopBtn.MouseDown(primaryPress)
	w.Canvas().Unfocus()
	test.Tap(h.stopBtn)

	if got, _ := h.binding.Get(); got != getAltName()+"+k" {
		t.Fatalf("saved %q", got)
	}
	if h.isCapturing || gate.disabled {
		t.Fatal("saving should end the capture")
	}
}

func TestARefusedSaveKeepsCapturing(t *testing.T) {
	h, gate, w := capturing(t)

	h.stopBtn.MouseDown(primaryPress)
	w.Canvas().Unfocus()
	test.Tap(h.stopBtn)

	if !h.isCapturing || !gate.disabled || w.Canvas().Focused() != h.entry {
		t.Fatal("an invalid combo should leave the capture open and ready for keys")
	}
	w.Canvas().Unfocus()
	if h.isCapturing {
		t.Fatal("the press on Save kept suppressing focus loss")
	}
}

func TestARightClickOnSaveEndsTheCapture(t *testing.T) {
	h, gate, w := capturing(t)

	h.stopBtn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	w.Canvas().Unfocus()

	if h.isCapturing || gate.disabled {
		t.Fatal("a right click on Save left the capture running and the global hotkeys off")
	}
}

// Letting go away from Save brings no tap.
func TestAPressThatLeavesSaveGivesTheFocusBack(t *testing.T) {
	h, gate, w := capturing(t)

	h.stopBtn.MouseDown(primaryPress)
	w.Canvas().Unfocus()
	h.stopBtn.MouseOut()

	if !h.isCapturing || w.Canvas().Focused() != h.entry {
		t.Fatal("the capture was left running without the focus")
	}
	w.Canvas().Unfocus()
	if h.isCapturing || gate.disabled {
		t.Fatal("losing the focus after the press left the global hotkeys off")
	}
}

func TestAPressThatLeavesAndComesBackToSaveSaves(t *testing.T) {
	h, gate, w := capturing(t)
	h.entry.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyK, Modifier: fyne.KeyModifierAlt})

	h.stopBtn.MouseDown(primaryPress)
	w.Canvas().Unfocus()
	h.stopBtn.MouseOut()
	h.stopBtn.MouseIn(primaryPress)
	h.stopBtn.MouseUp(primaryPress)
	w.Canvas().Unfocus()
	test.Tap(h.stopBtn)

	if got, _ := h.binding.Get(); got != getAltName()+"+k" {
		t.Fatalf("saved %q", got)
	}
	if h.isCapturing || gate.disabled {
		t.Fatal("saving should end the capture")
	}
}

func TestAStaleHoldOnSaveDoesNotSwallowTheNextFocusLoss(t *testing.T) {
	h, gate, w := capturing(t)

	h.stopBtn.MouseDown(primaryPress)
	w.Canvas().Unfocus()
	h.stopBtn.MouseOut()
	h.stopBtn.MouseIn(&desktop.MouseEvent{})
	h.stopBtn.MouseUp(primaryPress)
	w.Canvas().Unfocus()

	if h.isCapturing || gate.disabled {
		t.Fatal("a release on Save with no press on it kept the capture running")
	}
}

var primaryPress = &desktop.MouseEvent{Button: desktop.MouseButtonPrimary}

func newHotkeysWindow(t *testing.T) (*MainWindow, *hotkeyGate) {
	t.Helper()
	a := withDefaultTheme(t)
	w := &MainWindow{Window: a.NewWindow("Encre"), app: a, config: config.Default()}
	w.later = func(time.Duration, func()) {}
	t.Cleanup(w.Close)
	w.hotkeyBindings = make(map[config.ActionKind]binding.String, len(config.ActionOrder))
	for _, kind := range config.ActionOrder {
		w.hotkeyBindings[kind] = binding.NewString()
	}
	section := w.createHotkeysSection()

	gate := &hotkeyGate{}
	for _, capture := range w.captures {
		capture.onCaptureStart = func() { gate.disabled = true }
		capture.onCaptureStop = func() { gate.disabled = false }
	}

	w.tabs = container.NewAppTabs(
		container.NewTabItem("Hotkeys", section),
		container.NewTabItem("Actions", widget.NewLabel("")),
	)
	w.tabs.OnSelected = w.onTabSelected
	w.SetContent(w.tabs)
	return w, gate
}

func TestHidingTheWindowEndsACapture(t *testing.T) {
	w, gate := newHotkeysWindow(t)
	capture := w.captures[config.ActionOrder[0]]
	capture.startCapture()

	w.HideWindow()

	if capture.isCapturing || gate.disabled {
		t.Fatal("closing the settings window left the global hotkeys off")
	}
}

func TestSwitchingTabsEndsACapture(t *testing.T) {
	w, gate := newHotkeysWindow(t)
	capture := w.captures[config.ActionOrder[0]]
	capture.startCapture()

	w.tabs.SelectIndex(1)

	if capture.isCapturing || gate.disabled {
		t.Fatal("switching tabs left the global hotkeys off")
	}
}

type pinnedColor struct {
	fyne.Theme
	input color.Color
}

func (p pinnedColor) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameInputBackground {
		return p.input
	}
	return p.Theme.Color(name, variant)
}

func TestTheCaptureFieldFollowsAThemeOverride(t *testing.T) {
	withDefaultTheme(t)
	h := NewHotkeyCapture(binding.NewString())
	want := color.NRGBA{R: 1, G: 2, B: 3, A: 255}
	override := container.NewThemeOverride(h, pinnedColor{Theme: theme.DefaultTheme(), input: want})
	w := test.NewWindow(override)
	defer w.Close()

	renderer := test.TempWidgetRenderer(t, h.entry).(*themedBackgroundRenderer)
	if got := renderer.BackgroundColor(); got != want {
		t.Fatalf("background = %v, want the override's %v", got, want)
	}
}

func TestOnlyKeysTheListenerCanReadAreRecorded(t *testing.T) {
	for key, want := range map[fyne.KeyName]string{
		fyne.KeyUnknown:  "ctrl+alt",
		fyne.KeyPlus:     "ctrl+alt",
		fyne.KeyMinus:    "ctrl+alt",
		fyne.KeyPageUp:   "ctrl+alt+pageup",
		fyne.KeyPageDown: "ctrl+alt+pagedown",
		fyne.KeyF12:      "ctrl+alt+f12",
	} {
		h, _, _ := capturing(t)
		h.entry.TypedShortcut(&desktop.CustomShortcut{KeyName: key, Modifier: fyne.KeyModifierControl | fyne.KeyModifierAlt})
		if h.entry.Text != want {
			t.Errorf("%q was captured as %q, want %q", key, h.entry.Text, want)
		}
	}
}

func TestLettersAreSavedWhereTheListenerReadsThem(t *testing.T) {
	cases := []struct {
		goos     string
		key      fyne.KeyName
		scancode int
		want     string
	}{
		{"linux", fyne.KeyZ, 29, "y"},   // QWERTZ Z sits on the US Y key
		{"linux", fyne.KeyA, 24, "q"},   // AZERTY A sits on the US Q key
		{"linux", fyne.KeyM, 47, ""},    // AZERTY M sits on the US semicolon, which the listener cannot match
		{"linux", fyne.Key1, 87, "1"},   // numpad 1
		{"linux", fyne.KeyF5, 71, "f5"}, // not positional
		{"darwin", fyne.KeyZ, 0x10, "y"},
		{"windows", fyne.KeyZ, 44, "z"}, // Windows matches by virtual key, which follows the layout
		{"linux", fyne.KeyZ, 0, "z"},    // no scancode known
	}
	for _, c := range cases {
		if got := bindingKeyOn(c.goos, c.key, c.scancode); got != c.want {
			t.Errorf("%s %q at %d is saved as %q, want %q", c.goos, c.key, c.scancode, got, c.want)
		}
	}
}

func TestTabIsRecordedRatherThanEndingTheCapture(t *testing.T) {
	h, _, _ := capturing(t)
	if !h.entry.AcceptsTab() {
		t.Fatal("Tab would move focus and end the capture")
	}
	h.stopCapture()
	if h.entry.AcceptsTab() {
		t.Fatal("Tab is kept from moving focus once the capture ended")
	}
}
