package ui

import (
	"fmt"
	"runtime"
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
		for _, size := range config.TextSizes {
			t.Run(fmt.Sprintf("%s-%s", style, size), func(t *testing.T) {
				card, app := newCard(t)
				variant := theme.VariantDark
				app.Settings().SetTheme(newAppTheme(&variant))
				card.SetStyle(style)
				card.SetTextSize(size)
				answered(card, "q", "Plain first.")
				answered(card, "q", "See [docs](https://go.dev).\n\n- an item with [a link](https://b.example)\n\n| a | b |\n|---|---|\n| [x](https://a.example) | y |")

				want := card.readingTheme().Color(theme.ColorNameHyperlink, variant)
				links := hyperlinksIn(card.content)
				if len(links) == 0 {
					t.Fatal("no links drawn")
				}
				wantSize := card.readingTheme().Size(theme.SizeNameText)
				for _, link := range links {
					if got := link.Theme().Color(theme.ColorNameHyperlink, variant); got != want {
						t.Errorf("%q is drawn in %v, want the design's %v", link.Text, got, want)
					}
					if got := link.Theme().Size(link.SizeName); got != wantSize {
						t.Errorf("%q is drawn at %v, want the card's %v", link.Text, got, wantSize)
					}
				}
				if len(links) != 3 {
					t.Errorf("found %d links, want the paragraph's, the list item's and the table's", len(links))
				}
			})
		}
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
		before = card.scroll.Offset.Y
		side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: -3, DY: -40}})
		if card.scroll.Offset.Y == before || side.Offset.X != 0 {
			t.Error("a mostly vertical swipe did not scroll the answer alone")
		}
		side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: -40}})
		if side.Offset.X == 0 {
			t.Error("a sideways wheel did not scroll the block")
		}
	}
}

// Each new theme scope makes Fyne parse and keep the fonts again, so rendering a streamed answer
// must not make one per render.
func TestStreamingAnAnswerWithLinksDoesNotGrowMemory(t *testing.T) {
	card, _ := newCard(t)
	update, _, _ := card.Open("q", "", func() {})
	answer := "Intro with [a link](https://go.dev).\n\n- item **one**\n- item `two`\n\n```\ncode\n```\n\n| a | b |\n|---|---|\n| [x](https://a.example) | y |\n\n"

	heap := func() uint64 {
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapInuse
	}
	update(answer, false)
	card.window.Canvas().(test.WindowlessCanvas).Capture()
	before := heap()
	for i := range 60 {
		update(answer+strings.Repeat("more words ", i), false)
		card.window.Canvas().(test.WindowlessCanvas).Capture()
	}
	update(answer+strings.Repeat("more words ", 60), true)
	card.window.Canvas().(test.WindowlessCanvas).Capture()
	if grown := int64(heap()) - int64(before); grown > 100<<20 {
		t.Errorf("rendering grew the heap by %d MB", grown>>20)
	}
}

// A list item measures its links in its own theme; in the app's, a link at a larger size runs past the card.
func TestAListItemMeasuresItsTextAtTheCardsSize(t *testing.T) {
	for _, size := range config.TextSizes {
		t.Run(string(size), func(t *testing.T) {
			card, _ := newCard(t)
			card.SetTextSize(size)
			answered(card, "q", "- see [a link whose words are many and long enough to wrap the line twice over](https://a.example) tail words after it")

			var item *widget.RichText
			for _, segment := range card.content.Segments {
				if list, ok := segment.(*cardList); ok {
					item = list.items[0].text
				}
			}
			if item == nil {
				t.Fatal("no list drawn")
			}
			want := theme.SizeForWidget(theme.SizeNameText, card.content)
			if got := theme.SizeForWidget(theme.SizeNameText, item); got != want {
				t.Fatalf("the item measures at %v, the card draws at %v", got, want)
			}
			for _, link := range hyperlinksIn(item) {
				if end := link.Position().X + link.Size().Width; end > item.Size().Width+0.5 {
					t.Errorf("%q ends at %v, past the item's %v", link.Text, end, item.Size().Width)
				}
			}
		})
	}
}

// A link redrawn on its own, as on hover, takes its look from the app theme; it must still be the card's.
func TestLinksKeepTheCardsLookAfterTheTextSizeChangesWithTheCardOpen(t *testing.T) {
	card, app := newCard(t)
	variant := theme.VariantDark
	app.Settings().SetTheme(newAppTheme(&variant))
	card.SetStyle(config.CardStyleMidnight)
	answered(card, "q", "- an item with [a link](https://b.example)")
	card.SetTextSize(config.TextSizeLarger)

	want := card.readingTheme()
	links := hyperlinksIn(card.content)
	if len(links) == 0 {
		t.Fatal("no links drawn")
	}
	for _, link := range links {
		link.Refresh()
		if got := link.Theme().Color(theme.ColorNameHyperlink, variant); got != want.Color(theme.ColorNameHyperlink, variant) {
			t.Errorf("%q is drawn in %v after the size change", link.Text, got)
		}
		if got := link.Theme().Size(link.SizeName); got != want.Size(theme.SizeNameText) {
			t.Errorf("%q is drawn at %v after the size change, want %v", link.Text, got, want.Size(theme.SizeNameText))
		}
	}
}

func TestABareAddressInATableCellIsALink(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "| site | note |\n|---|---|\n| see https://go.dev today | x |")
	links := hyperlinksIn(card.content)
	if len(links) != 1 || links[0].URL == nil || links[0].URL.String() != "https://go.dev" {
		t.Fatalf("links in the table: %v", links)
	}
}
