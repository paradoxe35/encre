package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
)

func hyperlinksIn(o fyne.CanvasObject) []*widget.Hyperlink {
	var found []*widget.Hyperlink
	switch o := o.(type) {
	case *widget.Hyperlink:
		return []*widget.Hyperlink{o}
	case *fyne.Container:
		for _, child := range o.Objects {
			found = append(found, hyperlinksIn(child)...)
		}
	case fyne.Widget:
		for _, child := range test.WidgetRenderer(o).Objects() {
			found = append(found, hyperlinksIn(child)...)
		}
	}
	return found
}

// Links are made anew at every render, after the reading theme was applied to what was there.
func TestLinksTakeTheCardDesignsColour(t *testing.T) {
	for _, style := range config.CardStyles {
		t.Run(string(style), func(t *testing.T) {
			card, app := newCard(t)
			variant := theme.VariantDark
			app.Settings().SetTheme(newAppTheme(&variant))
			card.SetStyle(style)
			answered(card, "q", "Plain first.")
			answered(card, "q", "See [docs](https://go.dev).\n\n| a | b |\n|---|---|\n| [x](https://a.example) | y |")

			want := card.readingTheme().Color(theme.ColorNameHyperlink, variant)
			links := hyperlinksIn(card.content)
			if len(links) == 0 {
				t.Fatal("no links drawn")
			}
			for _, link := range links {
				if got := link.Theme().Color(theme.ColorNameHyperlink, variant); got != want {
					t.Errorf("%q is drawn in %v, want the design's %v", link.Text, got, want)
				}
			}
		})
	}
}

func sideScrollsIn(o fyne.CanvasObject) []*sideScroll {
	switch o := o.(type) {
	case *sideScroll:
		return []*sideScroll{o}
	case *fyne.Container:
		var found []*sideScroll
		for _, child := range o.Objects {
			found = append(found, sideScrollsIn(child)...)
		}
		return found
	case fyne.Widget:
		var found []*sideScroll
		for _, child := range test.WidgetRenderer(o).Objects() {
			found = append(found, sideScrollsIn(child)...)
		}
		return found
	}
	return nil
}

func TestTheWheelOverWideCodeScrollsTheAnswer(t *testing.T) {
	card, _ := newCard(t)
	wide := strings.Repeat("x", 400)
	answered(card, "q", strings.Repeat("A long answer line.\n\n", 60)+"```\n"+wide+"\n```\n\n| "+wide+" | b |\n|---|---|\n| c | d |\n\n"+strings.Repeat("More.\n\n", 30))

	sides := sideScrollsIn(card.content)
	if len(sides) != 2 {
		t.Fatalf("found %d sideways scrolls, want the code block's and the table's", len(sides))
	}
	for _, side := range sides {
		before := card.scroll.Offset.Y
		side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -40}})
		if card.scroll.Offset.Y == before || side.Offset.X != 0 {
			t.Errorf("the wheel moved the answer %v→%v and the block to %v, want only the answer moved", before, card.scroll.Offset.Y, side.Offset.X)
		}
		side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: -40}})
		if side.Offset.X == 0 {
			t.Error("a sideways wheel did not scroll the block")
		}
	}
}
