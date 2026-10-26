package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/paradoxe35/encre/internal/stt"
	"github.com/paradoxe35/encre/internal/testhome"
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

func TestAnEmptyModelFilterSaysSo(t *testing.T) {
	test.NewApp()
	testhome.Use(t)
	list := NewModelList(stt.NewStore(), test.NewWindow(nil), "", nil)

	list.filter.SetSelected("Downloaded")
	if !list.empty.Visible() || list.empty.Text != "No models downloaded yet" {
		t.Fatalf("an empty Downloaded view shows %q (visible %v)", list.empty.Text, list.empty.Visible())
	}
	list.filter.SetSelected("All")
	list.search.SetText("no model is called this")
	if !list.empty.Visible() || list.empty.Text != "No models match" {
		t.Fatalf("a search matching nothing shows %q (visible %v)", list.empty.Text, list.empty.Visible())
	}
	list.search.SetText("")
	if list.empty.Visible() {
		t.Fatal("the empty note stays over a full list")
	}
}
