package ui

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func answered(card *AnswerCard, question, answer string) {
	card.Open(question, func() {})
	card.Update(answer, true)
}

func TestAnswerCardShowsTheAnswerAndEscapeDismissesIt(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)

	var shown, hidden int
	card.SetShowHideCallbacks(func() { shown++ }, func() { hidden++ })

	answered(card, "what is the capital of france", "**Paris**.")
	if !card.Visible() {
		t.Fatal("the card is not showing")
	}
	got := card.content.String()
	if !strings.HasPrefix(got, "what is the capital of france") || !strings.Contains(got, "Paris") {
		t.Fatalf("card reads %q", got)
	}

	answered(card, "and of spain", "Madrid.")
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

func TestStreamedWordsAreBatchedAndTheLastOneShown(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)
	var batch func()
	card.later = func(_ time.Duration, run func()) { batch = run }

	card.Open("q", func() {})
	card.Update("Paris", false)
	card.Update("Paris is", false)
	if got := card.content.String(); strings.Contains(got, "Paris is") || batch == nil {
		t.Fatalf("rendered every word at once: %q", got)
	}

	batch()
	if got := card.content.String(); !strings.Contains(got, "Paris is") {
		t.Fatalf("a batched update never showed: %q", got)
	}

	card.Update("Paris is the capital.", true)
	if got := card.content.String(); !strings.Contains(got, "the capital.") {
		t.Fatalf("the finished answer waited for the next batch: %q", got)
	}
}

func TestClosingAnAnswerBeingWrittenStopsIt(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)

	stopped := 0
	card.Open("q", func() { stopped++ })
	card.Update("Paris", false)
	card.Hide()
	if stopped != 1 {
		t.Fatalf("stop ran %d times, want once", stopped)
	}

	card.Open("q", func() { stopped++ })
	card.Update("Paris.", true)
	card.Hide()
	if stopped != 1 {
		t.Fatal("closing a finished answer stopped it again")
	}
}

func TestAnswerCardCopiesTheWholeAnswer(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)
	answered(card, "q", "line one\n\n- line two")

	test.Tap(card.copy)
	if got := app.Clipboard().Content(); got != "line one\n\n- line two" {
		t.Fatalf("clipboard holds %q", got)
	}
}

func TestALongQuestionDoesNotGrowTheFooter(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)

	answered(card, "q", "a")
	footer := card.footer.MinSize().Height
	answered(card, strings.Repeat("a long spoken question ", 30), "a")

	if card.footer.MinSize().Height != footer {
		t.Fatal("the footer grew with the question")
	}
}

func TestLongAnswersStopGrowingTheCard(t *testing.T) {
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)

	answered(card, "q", "short")
	short := card.window.Canvas().Size().Height

	answered(card, "q", strings.Repeat("A long paragraph that keeps going. ", 200))
	long := card.window.Canvas().Size().Height

	if long <= short || long > answerMaxHeight {
		t.Fatalf("short %v, long %v, cap %v", short, long, answerMaxHeight)
	}
}
