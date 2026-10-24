package ui

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// at is the place just before the first rune of text, in the answer's own coordinates.
func at(t *testing.T, s *selectableText, text string) fyne.Position {
	t.Helper()
	s.collect()
	for _, run := range s.runs {
		if i := strings.Index(run.text.Text, text); i >= 0 {
			prefix := len([]rune(run.text.Text[:i]))
			return run.pos.AddXY(run.width(prefix)+0.5, run.size.Height/2)
		}
	}
	t.Fatalf("no drawn text holds %q", text)
	return fyne.Position{}
}

// after is the place just past the last rune of text.
func after(t *testing.T, s *selectableText, text string) fyne.Position {
	t.Helper()
	s.collect()
	for _, run := range s.runs {
		if i := strings.Index(run.text.Text, text); i >= 0 {
			end := len([]rune(run.text.Text[:i+len(text)]))
			return run.pos.AddXY(run.width(end)-0.5, run.size.Height/2)
		}
	}
	t.Fatalf("no drawn text holds %q", text)
	return fyne.Position{}
}

func drag(s *selectableText, from, to fyne.Position) {
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: from}, Button: desktop.MouseButtonPrimary})
	s.pointer.Dragged(dragTo(s, to, fyne.NewDelta(to.X-from.X, to.Y-from.Y)))
	s.pointer.DragEnd()
}

// dragTo is a drag event whose absolute position is the true one, as glfw's is.
func dragTo(s *selectableText, to fyne.Position, moved fyne.Delta) *fyne.DragEvent {
	origin := fyne.CurrentApp().Driver().AbsolutePositionForObject(s)
	return &fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to, AbsolutePosition: origin.Add(to)}, Dragged: moved}
}

func click(s *selectableText, point fyne.Position) {
	event := &desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point}, Button: desktop.MouseButtonPrimary}
	s.pointer.MouseDown(event)
	s.pointer.MouseUp(event)
}

func TestDraggingSelectsAcrossParagraphsAndCopiesWhatWasSelected(t *testing.T) {
	card, app := newCard(t)
	answered(card, "q", "First paragraph, long enough to wrap across more than one line of the answer card for certain.\n\nSecond **bold** paragraph.")
	s := card.selection

	drag(s, at(t, s, "long"), after(t, s, "bold"))
	want := "long enough to wrap across more than one line of the answer card for certain.\nSecond bold"
	if got := s.SelectedText(); got != want {
		t.Fatalf("selected %q, want %q", got, want)
	}
	if len(s.marks.Objects) == 0 {
		t.Fatal("the selection is not marked")
	}

	s.TypedShortcut(&fyne.ShortcutCopy{})
	if got := app.Clipboard().Content(); got != want {
		t.Fatalf("copied %q", got)
	}
}

func TestDraggingUpwardsSelectsTheSame(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Alpha beta gamma.")
	s := card.selection

	drag(s, after(t, s, "gamma"), at(t, s, "beta"))
	if got := s.SelectedText(); got != "beta gamma" {
		t.Fatalf("selected %q", got)
	}
}

func TestSelectedCodeKeepsItsLinesAndIndents(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Run:\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n\nDone.")
	s := card.selection

	drag(s, at(t, s, "func"), after(t, s, "}"))
	if got, want := s.SelectedText(), "func main() {\n\tfmt.Println(\"hi\")\n}"; got != want {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

func TestSelectedLinksListsAndTablesReadAsTheyShow(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "See [the docs](https://example.org) now.\n\n- one\n- two\n\n| a | b |\n|---|---|\n| 1 | 2 |")
	s := card.selection

	s.SelectAll()
	got := s.SelectedText()
	for _, want := range []string{"See the docs now.", "one\n", "two", "a\tb\n1\t2"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not read %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "q\n") {
		t.Errorf("select all left out the question: %q", got)
	}
}

func TestDoubleClickSelectsAWordAndTripleClickItsLine(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Alpha beta gamma.")
	s := card.selection

	point := at(t, s, "eta")
	s.pointer.DoubleTapped(&fyne.PointEvent{Position: point})
	if got := s.SelectedText(); got != "beta" {
		t.Fatalf("double click selected %q", got)
	}
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point}, Button: desktop.MouseButtonPrimary})
	if got := s.SelectedText(); got != "Alpha beta gamma." {
		t.Fatalf("triple click selected %q", got)
	}
}

func TestAClickClearsTheSelectionAndShiftClickExtendsIt(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Alpha beta gamma.")
	s := card.selection

	drag(s, at(t, s, "Alpha"), after(t, s, "Alpha"))
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: after(t, s, "beta")}, Button: desktop.MouseButtonPrimary, Modifier: fyne.KeyModifierShift})
	if got := s.SelectedText(); got != "Alpha beta" {
		t.Fatalf("shift click selected %q", got)
	}
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "gamma")}, Button: desktop.MouseButtonPrimary})
	if got := s.SelectedText(); got != "" || len(s.marks.Objects) != 0 {
		t.Fatalf("a click left %q selected", got)
	}
}

func TestTheCopyKeyInTheInputCopiesTheSelectionOrTheWholeAnswer(t *testing.T) {
	card, app := newCard(t)
	answered(card, "q", "Alpha **beta** gamma.")
	s := card.selection

	card.input.TypedShortcut(&fyne.ShortcutCopy{Clipboard: app.Clipboard()})
	if got := app.Clipboard().Content(); got != "Alpha **beta** gamma." {
		t.Fatalf("with nothing selected the copy key copied %q", got)
	}

	drag(s, at(t, s, "beta"), after(t, s, "gamma"))
	card.focusInput()
	card.input.TypedShortcut(&fyne.ShortcutCopy{Clipboard: app.Clipboard()})
	if got := app.Clipboard().Content(); got != "beta gamma" {
		t.Fatalf("copied %q", got)
	}

	card.input.SetText("my question")
	card.input.TypedShortcut(&fyne.ShortcutSelectAll{})
	card.input.TypedShortcut(&fyne.ShortcutCopy{Clipboard: app.Clipboard()})
	if got := app.Clipboard().Content(); got != "my question" {
		t.Fatalf("the input's own selection copied %q", got)
	}
}

func TestCopyingNothingKeepsTheClipboard(t *testing.T) {
	card, app := newCard(t)
	app.Clipboard().SetContent("kept")
	card.Open("q", "", func() {})

	card.input.TypedShortcut(&fyne.ShortcutCopy{Clipboard: app.Clipboard()})
	if got := app.Clipboard().Content(); got != "kept" {
		t.Fatalf("the clipboard holds %q", got)
	}
}

func TestANewAnswerDropsTheSelection(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Alpha beta gamma.")
	card.selection.SelectAll()

	answered(card, "next", "Delta.")
	if got := card.selection.SelectedText(); got != "" || len(card.selection.marks.Objects) != 0 {
		t.Fatalf("the new answer starts with %q selected", got)
	}
}

func TestTheSelectionHoldsWhileTheAnswerStreams(t *testing.T) {
	card, _ := newCard(t)
	update, _, _ := card.Open("q", "", func() {})
	update("Alpha beta", false)
	card.render()
	s := card.selection
	drag(s, at(t, s, "Alpha"), after(t, s, "Alpha"))

	update("Alpha beta gamma delta", true)
	if got := s.SelectedText(); got != "Alpha" {
		t.Fatalf("after more text arrived %q is selected", got)
	}
}

func TestTypingAfterSelectingGoesToTheInput(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Alpha.")
	s := card.selection
	drag(s, at(t, s, "Alpha"), after(t, s, "Alpha"))

	test.Type(s, "why")
	if card.input.Text != "why" || card.window.Canvas().Focused() != card.input {
		t.Fatalf("the input reads %q", card.input.Text)
	}
}

func TestEscapeWhileSelectingClosesTheCard(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Alpha.")
	card.selection.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if card.Visible() {
		t.Fatal("escape left the card open")
	}
}

func TestTheMarksOfAWideCodeLineStayWithinItsScroll(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "```\n"+strings.Repeat("wide_code_", 30)+"\n```")
	s := card.selection
	s.SelectAll()

	edge := s.Size().Width
	for _, mark := range s.marks.Objects {
		if end := mark.Position().X + mark.Size().Width; end > edge+0.5 {
			t.Fatalf("a mark ends at %v, past the answer's edge at %v", end, edge)
		}
	}
	if !strings.Contains(s.SelectedText(), strings.Repeat("wide_code_", 30)) {
		t.Fatal("the hidden part of the line was not selected")
	}
}

func TestAnEmptyAnswerSelectsNothing(t *testing.T) {
	card, _ := newCard(t)
	card.Prompt()
	s := card.selection
	s.SelectAll()
	drag(s, fyne.NewPos(0, 0), fyne.NewPos(50, 50))
	s.pointer.DoubleTapped(&fyne.PointEvent{Position: fyne.NewPos(5, 5)})
	if got := s.SelectedText(); got != "" {
		t.Fatalf("an empty card selected %q", got)
	}
}

func TestTheSelectionStartsWhereThePressWasNotWhereTheDragWasNoticed(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Alpha beta gamma.")
	s := card.selection

	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "Alpha")}, Button: desktop.MouseButtonPrimary})
	to := after(t, s, "beta")
	s.pointer.Dragged(dragTo(s, to, fyne.NewDelta(2, 0)))
	s.pointer.Dragged(dragTo(s, to, fyne.NewDelta(0, 0)))
	s.pointer.MouseUp(&desktop.MouseEvent{})
	s.pointer.DragEnd()
	if got := s.SelectedText(); got != "Alpha beta" {
		t.Fatalf("selected %q", got)
	}
}

func TestTheLinesAroundALinkSelectAndTheLinkStillOpens(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "A first line long enough to wrap over onto the next one of the card, so that [the docs](https://example.org/docs) start a line.")
	s := card.selection

	link, _ := s.linkAt(at(t, s, "docs"))
	if link == nil {
		t.Fatal("found no link under its text")
	}
	opened := false
	link.OnTapped = func() { opened = true }

	beside := at(t, s, "the docs").SubtractXY(0, 12)
	if within, _ := s.linkAt(beside); within != nil {
		t.Fatal("the point beside the link is over its text")
	}
	s.pointer.DoubleTapped(&fyne.PointEvent{Position: beside})
	if s.SelectedText() == "" {
		t.Fatal("a double click beside the link selected nothing")
	}
	s.pressed = time.Time{}
	click(s, beside)
	if opened {
		t.Fatal("a tap beside the link opened it")
	}

	s.pointer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "docs")}})
	if s.pointer.Cursor() != desktop.PointerCursor {
		t.Fatal("the pointer over the link is not a hand")
	}
	s.pressed = time.Time{}
	click(s, at(t, s, "docs"))
	if !opened {
		t.Fatal("tapping the link did not open it")
	}
	s.pointer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "first")}})
	if s.pointer.Cursor() != desktop.TextCursor {
		t.Fatal("the pointer over text is not a text cursor")
	}
}

func TestTextSideBySideIsMarkedAsOne(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Plain **bold** and [a link](https://example.org) end.")
	s := card.selection
	drag(s, at(t, s, "Plain"), after(t, s, "end."))
	if len(s.marks.Objects) != 1 {
		t.Fatalf("one line is marked in %d pieces", len(s.marks.Objects))
	}
}

func TestDoubleAndTripleClickPastALineStayOnIt(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Hello\n\nWorld here")
	s := card.selection

	beyond := after(t, s, "Hello").AddXY(40, 0)
	s.pointer.DoubleTapped(&fyne.PointEvent{Position: beyond})
	if got := s.SelectedText(); got != "Hello" {
		t.Fatalf("double click past the line selected %q", got)
	}
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: beyond}, Button: desktop.MouseButtonPrimary})
	if got := s.SelectedText(); got != "Hello" {
		t.Fatalf("triple click past the line selected %q", got)
	}
}

func TestDoubleClickSelectsTheWordUnderThePoint(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "Some **bold**, rest")
	s := card.selection

	s.pointer.DoubleTapped(&fyne.PointEvent{Position: after(t, s, "bold").SubtractXY(1, 0)})
	if got := s.SelectedText(); got != "bold" {
		t.Fatalf("double click on the end of bold selected %q", got)
	}
}

func TestDoubleClickSelectsAWordSplitAcrossStyles(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "A **bo**ld word")
	s := card.selection

	s.pointer.DoubleTapped(&fyne.PointEvent{Position: at(t, s, "ld")})
	if got := s.SelectedText(); got != "bold" {
		t.Fatalf("double click selected %q", got)
	}
}

func TestDraggingPastTheBottomKeepsScrolling(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", strings.Repeat("A paragraph of text that takes up a line or two of the card.\n\n", 40)+"The end.")
	s := card.selection
	var later []func()
	card.later = func(_ time.Duration, run func()) { later = append(later, run) }

	driver := fyne.CurrentApp().Driver()
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "paragraph")}, Button: desktop.MouseButtonPrimary})
	below := driver.AbsolutePositionForObject(card.scroll).AddXY(20, card.scroll.Size().Height+30)
	// glfw keeps the position relative to where the drag began.
	event := &fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: below, Position: below.Subtract(driver.AbsolutePositionForObject(s))}}
	s.pointer.Dragged(event)
	first := card.scroll.Offset.Y
	if first <= 0 {
		t.Fatal("dragging past the bottom did not scroll")
	}
	s.pointer.Dragged(event)
	second := card.scroll.Offset.Y
	if second <= first {
		t.Fatalf("a second move past the bottom stayed at %v", second)
	}

	if len(later) != 1 {
		t.Fatalf("%d scrolls are pending while the button is held", len(later))
	}
	run := later[0]
	later = nil
	run()
	if card.scroll.Offset.Y <= second {
		t.Fatal("holding the button past the bottom did not keep scrolling")
	}
	if len(later) != 1 {
		t.Fatal("the scroll does not go on while the button is held")
	}
	s.pointer.DragEnd()
	held := card.scroll.Offset.Y
	run = later[0]
	later = nil
	run()
	if card.scroll.Offset.Y != held || len(later) != 0 {
		t.Fatal("the scroll went on after the button was let go")
	}
}

// codeBar is the scrollbar of the one wide code block in the answer.
func codeBar(t *testing.T, s *selectableText) *innerBar {
	t.Helper()
	s.collect()
	if len(s.bars) != 1 {
		t.Fatalf("found %d scrollbars in the answer", len(s.bars))
	}
	return &s.bars[0]
}

func TestTheScrollbarOfWideCodeDragsAndExpands(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "```\n"+strings.Repeat("wide_code_", 30)+"\n```\n\nAfter.")
	s := card.selection
	bar := codeBar(t, s)
	scroll, thin := bar.scroll, bar.area.Size().Height

	s.pointer.MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: bar.pos.AddXY(5, thin/2)}})
	bar = codeBar(t, s)
	if bar.area.Size().Height <= thin {
		t.Fatal("hovering the scrollbar did not widen it")
	}
	if s.pointer.Cursor() == desktop.TextCursor {
		t.Fatal("the pointer over the scrollbar is a text cursor")
	}
	strip := bar.pos.AddXY(5, bar.area.Size().Height/2)
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: strip}, Button: desktop.MouseButtonPrimary})
	s.pointer.Dragged(dragTo(s, strip.AddXY(40, 0), fyne.NewDelta(40, 0)))
	s.pointer.DragEnd()
	if scroll.Offset.X <= 0 {
		t.Fatal("dragging the scrollbar did not scroll the code")
	}
	if got := s.SelectedText(); got != "" {
		t.Fatalf("dragging the scrollbar selected %q", got)
	}

	s.pointer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "After")}})
	if codeBar(t, s).area.Size().Height > thin {
		t.Fatal("the scrollbar stayed wide after the pointer left it")
	}
	drag(s, at(t, s, "wide_code_wide"), after(t, s, "After"))
	if got := s.SelectedText(); !strings.HasPrefix(got, "wide_code_wide") {
		t.Fatalf("dragging over the code selected %q", got)
	}
}

func TestTheMarksFollowScrolledCode(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "```\n"+strings.Repeat("wide_code_", 30)+"\n```")
	s := card.selection
	scroll := codeBar(t, s).scroll
	drag(s, at(t, s, "wide_code_"), at(t, s, "e_code_wide"))
	end := func() float32 {
		mark := s.marks.Objects[len(s.marks.Objects)-1]
		return mark.Position().X + mark.Size().Width
	}
	before := end()

	scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(-30, 0)})
	if got := end(); got > before-29 {
		t.Fatalf("the mark ends at %v after the code scrolled, was %v", got, before)
	}
}

func TestALinkPressedAsTheAnswerStreamsStillOpens(t *testing.T) {
	card, _ := newCard(t)
	update, _, _ := card.Open("q", "", func() {})
	update("See [the docs](https://example.org) now", false)
	card.render()
	s := card.selection
	point := at(t, s, "docs")
	s.pressed = time.Time{}
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point}, Button: desktop.MouseButtonPrimary})

	update("See [the docs](https://example.org) now, and then some more.", false)
	card.render()
	link, _ := s.linkAt(point)
	if link == nil {
		t.Fatal("test setup: no link under the pointer after the render")
	}
	opened := false
	link.OnTapped = func() { opened = true }
	s.pointer.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point}, Button: desktop.MouseButtonPrimary})
	if !opened {
		t.Fatal("a render between press and release kept the link from opening")
	}
}

func TestALinkOpensOnReleaseAndOnlyOnce(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "See [the docs](https://example.org) now.")
	s := card.selection
	link, _ := s.linkAt(at(t, s, "docs"))
	opened := 0
	link.OnTapped = func() { opened++ }
	point := at(t, s, "docs")

	click(s, point)
	if opened != 1 {
		t.Fatalf("a click opened the link %d times", opened)
	}
	click(s, point)
	s.pointer.DoubleTapped(&fyne.PointEvent{Position: point})
	if got := s.SelectedText(); got != "docs" {
		t.Fatalf("double click on the link selected %q", got)
	}
	click(s, point)
	if got := s.SelectedText(); got != "See the docs now." {
		t.Fatalf("triple click on the link selected %q", got)
	}
	if opened != 1 {
		t.Fatalf("clicking the link again opened it %d times", opened)
	}

	s.pressed = time.Time{}
	drag(s, point, at(t, s, "now"))
	s.pointer.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point}, Button: desktop.MouseButtonPrimary})
	s.pressed = time.Time{}
	s.pointer.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point}, Button: desktop.MouseButtonPrimary, Modifier: fyne.KeyModifierShift})
	s.pointer.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point}, Button: desktop.MouseButtonPrimary})
	if opened != 1 {
		t.Fatal("a drag or shift click opened the link")
	}
}

func TestTheCursorFollowsNewContent(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "See [the docs](https://example.org) now.")
	s := card.selection
	s.pointer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "docs")}})

	answered(card, "q", "Plain text without any link in it at all.")
	if s.hovered != nil {
		t.Fatal("the link of the old answer is still hovered")
	}
	if s.pointer.Cursor() != desktop.TextCursor {
		t.Fatal("the pointer over plain text is a hand")
	}
}

func TestALinkStaysHoveredWhileTheAnswerStreams(t *testing.T) {
	card, _ := newCard(t)
	update, _, _ := card.Open("q", "", func() {})
	update("See [the docs](https://example.org) now", false)
	card.render()
	s := card.selection
	s.pointer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "docs")}})
	if s.hovered == nil || !underlined(s.hovered) {
		t.Fatal("test setup: the link under the pointer is not hovered")
	}

	update("See [the docs](https://example.org) now, and then some more.", true)
	link, _ := s.linkAt(s.pointerAt)
	if link == nil || s.hovered != link || !underlined(link) {
		t.Fatal("a render dropped the hover of the link still under the pointer")
	}
}

func TestALinkMovedFromUnderTheRestingPointerIsNoLongerHovered(t *testing.T) {
	card, _ := newCard(t)
	update, _, _ := card.Open("q", "", func() {})
	update("See [the docs](https://example.org) now", false)
	card.render()
	s := card.selection
	s.pointer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "docs")}})
	if s.hovered == nil {
		t.Fatal("test setup: the link under the pointer is not hovered")
	}

	s.Resize(s.Size().AddWidthHeight(0, 40))
	update("See [the docs](https://example.org) now, and then some more.", true)
	if s.hovered != nil {
		t.Fatal("the link stayed hovered after the text moved under a pointer that did not")
	}
}

// underlined reads the line Fyne draws last under a hovered link.
func underlined(link *widget.Hyperlink) bool {
	renderer, _ := cachedRenderer(link)
	objects := renderer.Objects()
	return objects[len(objects)-1].Visible()
}

func TestCopiedTasksKeepTheirState(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", "- [x] done\n- [ ] todo")
	s := card.selection
	s.SelectAll()
	got := s.SelectedText()
	for _, want := range []string{"[x] done\n", "[ ] todo"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not read %q", got, want)
		}
	}
}

func TestTheSelectionDropsWhenTheTextBeforeItChanges(t *testing.T) {
	card, _ := newCard(t)
	update, _, _ := card.Open("q", "", func() {})
	update("**bo", false)
	card.render()
	s := card.selection
	drag(s, at(t, s, "bo"), after(t, s, "bo"))
	if got := s.SelectedText(); got != "bo" {
		t.Fatalf("selected %q", got)
	}

	update("**bold** text", true)
	if got := s.SelectedText(); got != "" {
		t.Fatalf("after the text before it changed %q is selected", got)
	}
}
