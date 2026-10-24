package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func TestATabHiddenAtAThemeChangeIsReThemedOnItsFirstVisit(t *testing.T) {
	a := withDefaultTheme(t)
	w := &MainWindow{Window: a.NewWindow("Encre"), app: a}
	t.Cleanup(w.Close)
	w.tabs = container.NewAppTabs(
		container.NewTabItem("System", widget.NewLabel("")),
		container.NewTabItem("Actions", widget.NewLabel("")),
	)
	w.tabs.OnSelected = w.onTabSelected
	w.later = func(_ time.Duration, run func()) { run() }
	w.SetContent(w.tabs)

	applied := 0
	a.Settings().AddListener(func(fyne.Settings) { applied++ })
	w.applyTheme("dark")
	applied = 0

	w.tabs.SelectIndex(1)
	if applied != 1 {
		t.Fatalf("the theme was applied %d times on the first visit, want 1", applied)
	}
	w.tabs.SelectIndex(0)
	w.tabs.SelectIndex(1)
	if applied != 1 {
		t.Errorf("the theme was applied %d times in all, want 1: later visits and the tab on screen need nothing", applied)
	}
}
