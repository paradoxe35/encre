package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
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
	s.pointer.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, to.Y-from.Y)})
	s.pointer.DragEnd()
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
	s.pointer.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(2, 0)})
	s.pointer.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(0, 0)})
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
	s.pointer.Tapped(&fyne.PointEvent{Position: beside})
	if opened {
		t.Fatal("a tap beside the link opened it")
	}

	s.pointer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(t, s, "docs")}})
	if s.pointer.Cursor() != desktop.PointerCursor {
		t.Fatal("the pointer over the link is not a hand")
	}
	s.pointer.Tapped(&fyne.PointEvent{Position: at(t, s, "docs")})
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
