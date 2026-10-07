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

// newCard batches nothing behind the test's back: a batch only runs when a test calls it.
func newCard(t *testing.T) (*AnswerCard, fyne.App) {
	t.Helper()
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)
	card.later = func(time.Duration, func()) {}
	return card, app
}

func answered(card *AnswerCard, question, answer string) {
	update, _ := card.Open(question, func() {})
	update(answer, true)
}

func TestAnswerCardShowsTheAnswerAndEscapeDismissesIt(t *testing.T) {
	card, _ := newCard(t)

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
	card, _ := newCard(t)
	var batch func()
	card.later = func(_ time.Duration, run func()) { batch = run }

	update, _ := card.Open("q", func() {})
	update("Paris", false)
	update("Paris is", false)
	if got := card.content.String(); strings.Contains(got, "Paris is") || batch == nil {
		t.Fatalf("rendered every word at once: %q", got)
	}

	batch()
	if got := card.content.String(); !strings.Contains(got, "Paris is") {
		t.Fatalf("a batched update never showed: %q", got)
	}

	update("Paris is the capital.", true)
	if got := card.content.String(); !strings.Contains(got, "the capital.") {
		t.Fatalf("the finished answer waited for the next batch: %q", got)
	}
}

func TestClosingAnAnswerBeingWrittenStopsIt(t *testing.T) {
	card, _ := newCard(t)

	stopped := 0
	update, _ := card.Open("q", func() { stopped++ })
	update("Paris", false)
	card.Hide()
	if stopped != 1 {
		t.Fatalf("stop ran %d times, want once", stopped)
	}

	update, _ = card.Open("q", func() { stopped++ })
	update("Paris.", true)
	card.Hide()
	if stopped != 1 {
		t.Fatal("closing a finished answer stopped it again")
	}
}

func TestAnswerCardCopiesTheWholeAnswer(t *testing.T) {
	card, app := newCard(t)
	answered(card, "q", "line one\n\n- line two")

	test.Tap(card.copy)
	if got := app.Clipboard().Content(); got != "line one\n\n- line two" {
		t.Fatalf("clipboard holds %q", got)
	}
}

func TestALongQuestionDoesNotGrowTheFooter(t *testing.T) {
	card, _ := newCard(t)

	answered(card, "q", "a")
	footer := card.footer.MinSize().Height
	answered(card, strings.Repeat("a long spoken question ", 30), "a")

	if card.footer.MinSize().Height != footer {
		t.Fatal("the footer grew with the question")
	}
}

func TestLongAnswersStopGrowingTheCard(t *testing.T) {
	card, _ := newCard(t)

	answered(card, "q", "short")
	short := card.window.Canvas().Size().Height

	answered(card, "q", strings.Repeat("A long paragraph that keeps going. ", 200))
	long := card.window.Canvas().Size().Height

	if long <= short || long > answerMaxHeight {
		t.Fatalf("short %v, long %v, cap %v", short, long, answerMaxHeight)
	}
}

func TestPromptPutsTheKeyboardInTheInput(t *testing.T) {
	card, _ := newCard(t)

	card.Prompt()
	if !card.Visible() || card.window.Canvas().Focused() != card.input {
		t.Fatal("the input does not have the keyboard")
	}
	if card.content.String() != "" || card.copy.Visible() {
		t.Fatal("an empty card offers something to read or copy")
	}
}

func TestEnterSendsAndShiftEnterBreaksTheLine(t *testing.T) {
	card, _ := newCard(t)
	var asked []string
	card.SetOnAsk(func(question string) { asked = append(asked, question) })
	card.Prompt()

	test.Type(card.input, "first line")
	card.input.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	card.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	card.input.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	test.Type(card.input, "second")
	if len(asked) != 0 || card.input.Text != "first line\nsecond" {
		t.Fatalf("shift+enter sent or lost the line break: %q, %v", card.input.Text, asked)
	}

	card.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(asked) != 1 || asked[0] != "first line\nsecond" || card.input.Text != "" {
		t.Fatalf("enter did not send the question: %v, left %q", asked, card.input.Text)
	}

	card.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(asked) != 1 {
		t.Fatal("an empty input was sent")
	}
}

func TestEscapeInTheInputClosesTheCard(t *testing.T) {
	card, _ := newCard(t)
	card.Prompt()

	card.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if card.Visible() {
		t.Fatal("escape left the card open")
	}
}

func TestAQuestionWaitingShowsThatAnAnswerIsComing(t *testing.T) {
	card, _ := newCard(t)

	card.Open("capital of france?", func() {})
	if got := card.content.String(); !strings.Contains(got, "Thinking") {
		t.Fatalf("card reads %q", got)
	}
}

func TestAFailureShowsBelowWhatArrived(t *testing.T) {
	card, _ := newCard(t)

	update, fail := card.Open("q", func() {})
	update("Paris is", false)
	fail("connection lost")
	got := card.content.String()
	if !strings.Contains(got, "Paris is") || !strings.Contains(got, "connection lost") || strings.Contains(got, "Thinking") {
		t.Fatalf("card reads %q", got)
	}
}

func TestANewerQuestionIgnoresTheOlderAnswer(t *testing.T) {
	card, _ := newCard(t)

	stopped := false
	older, _ := card.Open("first", func() { stopped = true })
	newer, _ := card.Open("second", func() {})
	older("late words from the first answer", true)
	newer("Second answer.", true)

	got := card.content.String()
	if !stopped || strings.Contains(got, "late words") || !strings.Contains(got, "Second answer.") {
		t.Fatalf("stopped %v, card reads %q", stopped, got)
	}
}

func TestAnEmptyCardIsOnlyItsInput(t *testing.T) {
	card, _ := newCard(t)
	card.Prompt()
	empty := card.size.Height
	if card.scroll.Visible() || empty < card.footer.MinSize().Height {
		t.Fatalf("an empty card of %v hides its input, or shows an empty scroll", empty)
	}

	if want := card.footer.MinSize().Height + 2*answerInset; empty != want {
		t.Fatalf("empty card is %v tall, want the input and equal space above and below: %v", empty, want)
	}

	answered(card, "q", "a")
	if empty >= card.size.Height {
		t.Fatalf("empty card %v tall, answered %v", empty, card.size.Height)
	}
}

func TestTheInputGrowsWithTheQuestionUpToALimit(t *testing.T) {
	card, _ := newCard(t)
	card.Prompt()
	one := card.footer.MinSize().Height

	card.input.SetText(strings.Repeat("a long question that wraps ", 4))
	two := card.footer.MinSize().Height
	card.input.SetText(strings.Repeat("a long question that wraps ", 40))
	most := card.footer.MinSize().Height

	if two <= one || most <= two || card.rows != maxInputRows {
		t.Fatalf("input heights %v, %v, %v with %d rows", one, two, most, card.rows)
	}
	card.input.SetText("")
	if card.footer.MinSize().Height != one {
		t.Fatal("the input did not shrink back once cleared")
	}
}

func TestRowsBreakBetweenWords(t *testing.T) {
	card, _ := newCard(t)
	card.Prompt()
	word := "abcdefgh"
	perLine := 0
	for line := word; card.input.rows(line) == 1; line += " " + word {
		perLine++
	}

	words := strings.TrimSpace(strings.Repeat(word+" ", perLine*2+1))
	if got := card.input.rows(words); got != 3 {
		t.Fatalf("%d words at %d a line took %d rows, want 3", perLine*2+1, perLine, got)
	}
	if got := card.input.rows("one\n\ntwo"); got != 3 {
		t.Fatalf("blank line counted as %d rows", got)
	}
}

func TestASpokenAnswerOpensWithoutTheInput(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "spoken question", "Answer.")

	if card.input.Visible() || card.send.Visible() {
		t.Fatal("a voice answer shows the typing input")
	}
	if !card.copy.Visible() {
		t.Fatal("a finished answer offers nothing to copy")
	}
}

func TestPromptAddsTheInputToAnAnswerForAFollowUp(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "spoken question", "Answer.")

	card.Prompt()
	if !card.input.Visible() || card.window.Canvas().Focused() != card.input {
		t.Fatal("the follow-up input is not ready to type in")
	}
	if !strings.Contains(card.content.String(), "Answer.") {
		t.Fatal("asking a follow-up cleared the answer being read")
	}
}

func TestATypedQuestionKeepsTheInputForTheNext(t *testing.T) {
	card, _ := newCard(t)
	card.Prompt()
	answered(card, "typed question", "Answer.")

	if !card.input.Visible() {
		t.Fatal("the input went away after a typed question was answered")
	}
}

// Esc is released in the hide callback, so every way of closing the card must run it. The window
// manager's close goes through the close intercept, which Fyne's test window cannot send.
func TestEveryWayOfClosingRunsTheHideCallback(t *testing.T) {
	closers := map[string]func(*AnswerCard){
		"close button": func(c *AnswerCard) { test.Tap(closeButton(t, c)) },
		"escape":       func(c *AnswerCard) { c.window.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape}) },
		"escape in the input": func(c *AnswerCard) {
			c.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
		},
		"global escape": func(c *AnswerCard) { c.Hide() },
	}

	for name, close := range closers {
		card, _ := newCard(t)
		shown, hidden := 0, 0
		card.SetShowHideCallbacks(func() { shown++ }, func() { hidden++ })

		card.Prompt()
		close(card)
		if card.Visible() || shown != 1 || hidden != 1 {
			t.Errorf("%s: visible %v, show ran %d, hide ran %d", name, card.Visible(), shown, hidden)
		}
	}
}

func closeButton(t *testing.T, card *AnswerCard) *widget.Button {
	t.Helper()
	for _, object := range card.footer.(*fyne.Container).Objects {
		if actions, ok := object.(*fyne.Container); ok {
			return actions.Objects[len(actions.Objects)-1].(*widget.Button)
		}
	}
	t.Fatal("no close button in the footer")
	return nil
}
