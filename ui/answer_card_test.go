package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestAnswerCardShowsTheAnswerAndEscapeDismissesIt(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)

	var shown, hidden int
	card.SetShowHideCallbacks(func() { shown++ }, func() { hidden++ })

	card.Show("what is the capital of france", "**Paris**.")
	if !card.Visible() || card.question.Text != "what is the capital of france" {
		t.Fatalf("visible %v, question %q", card.Visible(), card.question.Text)
	}
	if got := card.answer.String(); !strings.Contains(got, "Paris") {
		t.Fatalf("answer reads %q", got)
	}

	card.Show("and of spain", "Madrid.")
	if shown != 1 {
		t.Fatalf("show callback ran %d times for a card already up", shown)
	}

	card.window.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if card.Visible() || hidden != 1 {
		t.Fatalf("visible %v after escape, hide callback ran %d times", card.Visible(), hidden)
	}

	card.Hide()
	if hidden != 1 {
		t.Fatal("hiding a hidden card ran the callback again")
	}
}

func TestAnswerCardCopiesTheWholeAnswer(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)
	card.Show("q", "line one\n\n- line two")

	test.Tap(card.copy)
	if got := app.Clipboard().Content(); got != "line one\n\n- line two" {
		t.Fatalf("clipboard holds %q", got)
	}
}

func TestLongAnswersStopGrowingTheCard(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)

	card.Show("q", "short")
	short := card.window.Canvas().Size().Height

	card.Show("q", strings.Repeat("A long paragraph that keeps going. ", 200))
	long := card.window.Canvas().Size().Height

	if long <= short || long > answerMaxHeight {
		t.Fatalf("short %v, long %v, cap %v", short, long, answerMaxHeight)
	}
}
