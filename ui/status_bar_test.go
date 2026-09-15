package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

func newStatusWindow(t *testing.T) (*MainWindow, *statusIcon) {
	t.Helper()
	w := &MainWindow{app: test.NewApp(), statusBinding: binding.NewString()}
	w.Window = test.NewWindow(nil)
	t.Cleanup(w.Window.Close)

	bar := w.createStatusBar()
	w.Window.SetContent(bar)

	var icon *statusIcon
	for _, object := range bar.(*fyne.Container).Objects {
		if candidate, ok := object.(*statusIcon); ok {
			icon = candidate
		}
	}
	if icon == nil {
		t.Fatal("status bar has no status icon")
	}
	return w, icon
}

func TestStatusIconOpensDetailOnlyForLongMessages(t *testing.T) {
	w, icon := newStatusWindow(t)

	w.statusBinding.Set("Settings saved")
	if icon.Active() || icon.Cursor() != desktop.DefaultCursor {
		t.Fatal("a short message should leave the icon inert")
	}
	icon.Tapped(nil)
	if w.Window.Canvas().Overlays().Top() != nil {
		t.Fatal("tapping the inert icon opened a dialog")
	}

	long := "Connection failed: " + strings.Repeat("the endpoint returned an unexpected response ", 3)
	w.statusBinding.Set(long)
	if !icon.Active() || icon.Cursor() != desktop.PointerCursor {
		t.Fatal("a long message should make the icon clickable")
	}
	icon.Tapped(nil)
	if w.Window.Canvas().Overlays().Top() == nil {
		t.Fatal("tapping the icon should open the detail dialog")
	}
}

func TestStatusThresholdCountsCharactersNotBytes(t *testing.T) {
	w, icon := newStatusWindow(t)

	w.statusBinding.Set(strings.Repeat("é", statusDetailThreshold))
	if icon.Active() {
		t.Fatal("70 accented characters are not over the limit")
	}
	w.statusBinding.Set(strings.Repeat("é", statusDetailThreshold+1))
	if !icon.Active() {
		t.Fatal("71 characters are over the limit")
	}
}
