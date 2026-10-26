package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func themeTabsWindow(t *testing.T) (*MainWindow, *int) {
	t.Helper()
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
	w.watchTheme()

	applied := 0
	a.Settings().AddListener(func(fyne.Settings) { applied++ })
	return w, &applied
}

func TestATabHiddenAtAThemeChangeIsReThemedOnItsFirstVisit(t *testing.T) {
	w, applied := themeTabsWindow(t)
	w.applyTheme("dark")
	*applied = 0

	w.tabs.SelectIndex(1)
	if *applied != 1 {
		t.Fatalf("the theme was applied %d times on the first visit, want 1", *applied)
	}
	w.tabs.SelectIndex(0)
	w.tabs.SelectIndex(1)
	if *applied != 1 {
		t.Errorf("the theme was applied %d times in all, want 1: later visits and the tab on screen need nothing", *applied)
	}
}

func TestASystemThemeChangeAlsoLeavesHiddenTabsToReTheme(t *testing.T) {
	w, applied := themeTabsWindow(t)
	w.app.Settings().SetTheme(w.app.Settings().Theme())
	*applied = 0

	w.tabs.SelectIndex(1)
	if *applied != 1 {
		t.Errorf("the theme was applied %d times on the first visit after a change Encre did not make, want 1", *applied)
	}
}

func TestSavingTheSameThemeLeavesTheTabsAlone(t *testing.T) {
	w, applied := themeTabsWindow(t)
	w.applyTheme("dark")
	w.tabs.SelectIndex(1)
	w.tabs.SelectIndex(0)
	*applied = 0

	w.applyTheme("dark")
	w.tabs.SelectIndex(1)
	if *applied != 0 {
		t.Errorf("an unchanged theme was applied %d times, want 0", *applied)
	}
}
