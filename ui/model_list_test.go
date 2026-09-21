package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/paradoxe35/encre/internal/stt"
)

func TestActiveModelTextRemainsVisibleForLongLists(t *testing.T) {
	models := []stt.Model{
		{ID: "small", Name: "Small English", SizeBytes: 1},
		{ID: "large", Name: "Large Multilingual", SizeBytes: 1},
	}
	downloaded := func(model stt.Model) bool { return model.ID == "large" }

	if got := activeModelText("large", models, downloaded); got != "Active model: Large Multilingual" {
		t.Fatalf("active model label = %q", got)
	}
	if got := activeModelText("small", models, downloaded); got != "No active model selected" {
		t.Fatalf("undownloaded model label = %q", got)
	}
	if got := activeModelText("missing", models, downloaded); got != "No active model selected" {
		t.Fatalf("missing model label = %q", got)
	}
}

// A catalogue refresh replaces the list underneath the widget; Reload must
// rebuild the rows and re-emit the active label without any user action.
func TestModelListReloadRebuildsRowsAndActiveLabel(t *testing.T) {
	test.NewApp()
	list := NewModelList(stt.NewStore(), test.NewWindow(nil), "", nil)

	var active string
	list.SetActiveChanged(func(text string) { active = text })

	list.mu.Lock()
	list.filtered = nil
	list.mu.Unlock()

	list.Reload()

	if got, want := list.count(), len(stt.Catalogue()); got != want {
		t.Errorf("list shows %d rows after Reload, want the whole catalogue (%d)", got, want)
	}
	if active != "No active model selected" {
		t.Errorf("active label after Reload = %q", active)
	}
}
