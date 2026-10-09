package ui

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
)

// newCard batches nothing behind the test's back: a batch only runs when a test calls it.
func newCard(t *testing.T) (*AnswerCard, fyne.App) {
	t.Helper()
	app := test.NewTempApp(t)
	card := NewAnswerCard(app)
	card.later = func(time.Duration, func()) {}
	card.animate = func(_, to fyne.Size, apply func(fyne.Size)) *fyne.Animation {
		apply(to)
		return nil
	}
	card.fade = func(_, to float64, apply func(float64), done func()) *fyne.Animation {
		apply(to)
		if done != nil {
			done()
		}
		return nil
	}
	return card, app
}

func answered(card *AnswerCard, question, answer string) {
	update, _, _ := card.Open(question, func() {})
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

	update, _, _ := card.Open("q", func() {})
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
	update, _, _ := card.Open("q", func() { stopped++ })
	update("Paris", false)
	card.Hide()
	if stopped != 1 {
		t.Fatalf("stop ran %d times, want once", stopped)
	}

	update, _, _ = card.Open("q", func() { stopped++ })
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

// A reader at the top of a long answer scrolls it with the wheel at once, with no pointer move
// first: a two-finger scroll moves none.
func TestTheWheelScrollsAnAnswerThatOutgrewTheCard(t *testing.T) {
	card, _ := newCard(t)
	var batch func()
	card.later = func(_ time.Duration, run func()) { batch = run }
	paragraph := "A long paragraph that keeps going. "

	update, _, _ := card.Open("q", func() {})
	update(strings.Repeat(paragraph, 200), false)
	batch()
	card.scroll.ScrollToTop()
	update(strings.Repeat(paragraph, 400), true)

	if laidOut, needed := card.content.Size().Height, card.content.MinSize().Height; laidOut < needed {
		t.Fatalf("the answer is laid out %v tall but needs %v, so the scroll sees nothing to scroll", laidOut, needed)
	}
	// Fyne aims the wheel at where it last saw the pointer, which may be anywhere on the card.
	wheel := card.window.Content().(*fyne.Container).Objects[0].(fyne.Scrollable)
	wheel.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(0, -100)})
	if card.scroll.Offset.Y <= 0 {
		t.Fatalf("the wheel left the answer at %v", card.scroll.Offset.Y)
	}
}

// streamInto opens a card and returns a way to send it more of a long answer, rendered at once.
func streamInto(t *testing.T) (*AnswerCard, func(paragraphs int, done bool)) {
	t.Helper()
	card, _ := newCard(t)
	var batch func()
	card.later = func(_ time.Duration, run func()) { batch = run }
	update, _, _ := card.Open("q", func() {})
	return card, func(paragraphs int, done bool) {
		update(strings.Repeat("A long paragraph that keeps going. ", paragraphs), done)
		if !done {
			batch()
		}
	}
}

// A reader who scrolled is left where they are while the answer keeps coming.
func TestAReaderKeepsTheirPlaceWhileTheAnswerGrows(t *testing.T) {
	card, stream := streamInto(t)
	stream(100, false)
	stream(200, false)
	card.scroll.ScrollToTop()

	card.scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(0, -120)})
	reading := card.scroll.Offset.Y
	stream(400, true)
	if card.scroll.Offset.Y != reading {
		t.Fatalf("the answer moved the reader from %v to %v", reading, card.scroll.Offset.Y)
	}
}

// An answer is read from its start: text arriving past the bottom of the card waits there.
func TestALongAnswerStaysAtItsStartAsItGrows(t *testing.T) {
	card, stream := streamInto(t)
	stream(100, false)
	stream(200, false)
	stream(400, true)
	if card.scroll.Offset.Y != 0 {
		t.Fatalf("the answer moved to %v as it grew past the card", card.scroll.Offset.Y)
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

	update, fail, _ := card.Open("q", func() {})
	update("Paris is", false)
	fail("connection lost")
	got := card.content.String()
	if !strings.Contains(got, "Paris is") || !strings.Contains(got, "Connection lost") || strings.Contains(got, "Thinking") {
		t.Fatalf("card reads %q", got)
	}
}

func TestANewerQuestionIgnoresTheOlderAnswer(t *testing.T) {
	card, _ := newCard(t)

	stopped := false
	older, _, _ := card.Open("first", func() { stopped = true })
	newer, _, _ := card.Open("second", func() {})
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
	if card.reading.Visible() || empty < card.footer.MinSize().Height {
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

func TestASpokenAnswerOffersTheInputForAFollowUp(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "spoken question", "Answer.")

	if !card.input.Visible() || !card.send.Visible() {
		t.Fatal("a voice answer hides the input")
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

func TestLargerTextGrowsTheAnswerAndWidensTheCard(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "q", strings.Repeat("A sentence to read. ", 12))
	before := card.size
	footer := card.footer.MinSize().Height

	card.SetTextSize(config.TextSizeLarger)
	if card.size.Width <= before.Width || card.size.Width > maxAnswerWidth {
		t.Fatalf("width %v after %v, cap %v", card.size.Width, before.Width, maxAnswerWidth)
	}
	if card.reading.Theme.Size(theme.SizeNameText) <= theme.DefaultTheme().Size(theme.SizeNameText) {
		t.Fatal("the answer text did not grow")
	}
	if card.footer.MinSize().Height != footer {
		t.Fatal("the input grew with the answer text")
	}

	card.SetTextSize(config.TextSizeSmall)
	if card.size.Width != answerWidth {
		t.Fatalf("small text widened the card to %v", card.size.Width)
	}
}

func TestASizeChangeGlidesWhileTheCardShows(t *testing.T) {
	card, _ := newCard(t)
	var glides []fyne.Size
	card.animate = func(from, to fyne.Size, apply func(fyne.Size)) *fyne.Animation {
		glides = append(glides, from, to)
		apply(to)
		return nil
	}

	answered(card, "q", "short")
	if len(glides) != 0 {
		t.Fatalf("the card glided open from nothing: %v", glides)
	}
	answered(card, "q", strings.Repeat("A longer answer that wraps. ", 10))
	if len(glides) != 2 || glides[1].Height <= glides[0].Height {
		t.Fatalf("growing did not glide from the old height to the new: %v", glides)
	}
}

func TestAStyleChangeWaitsForTheCardToClose(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "Q", "A")
	shown := card.window

	if !designFor(card.style).glass {
		t.Fatal("a new card is not glass, the default")
	}

	card.SetStyle(config.CardStyleSolid)
	if card.window != shown {
		t.Fatal("the card was rebuilt while it showed")
	}
	if !designFor(card.style).glass {
		t.Fatal("the open card took on the new style before it was rebuilt")
	}

	card.Hide()
	answered(card, "Q", "A")
	if card.window == shown {
		t.Fatal("the card kept its old window after closing")
	}
	if designFor(card.style).glass {
		t.Fatal("the rebuilt card is still glass")
	}
}

func TestTheGlassCardFollowsTheAppVariant(t *testing.T) {
	card, app := newCard(t)
	variant := theme.VariantLight
	app.Settings().SetTheme(newAppTheme(&variant))

	card.SetStyle(config.CardStyleGlass)
	glass := card.cardTheme().Color(colorNameCard, theme.VariantDark)
	card.SetStyle(config.CardStyleSolid)
	solid := card.cardTheme().Color(colorNameCard, theme.VariantLight)

	if glass == solid {
		t.Fatal("the glass card took the solid card's dark colours in a light app")
	}
}

func TestListItemsTakeTheCardsTextSizeAndColours(t *testing.T) {
	card, app := newCard(t)
	variant := theme.VariantLight
	app.Settings().SetTheme(newAppTheme(&variant))
	card.SetTextSize(config.TextSizeLarger)
	answered(card, "Q", "Intro\n\n- first item\n- second item")

	reading := card.readingTheme()
	for _, text := range []string{"Intro", "first item", "• "} {
		segment := findText(card.content.Segments, text)
		if segment == nil {
			t.Fatalf("no text %q in the answer", text)
		}
		visual := segment.Visual().(*canvas.Text)
		if want := reading.Size(theme.SizeNameText); visual.TextSize != want {
			t.Fatalf("%q is %v points, want the card's %v", text, visual.TextSize, want)
		}
		if want := reading.Color(theme.ColorNameForeground, variant); visual.Color != want {
			t.Fatalf("%q is drawn in %v, want the dark card's %v", text, visual.Color, want)
		}
	}
}

func findText(segments []widget.RichTextSegment, text string) *widget.TextSegment {
	for _, segment := range segments {
		switch segment := segment.(type) {
		case *widget.TextSegment:
			if strings.HasPrefix(segment.Text, text) {
				return segment
			}
		case widget.RichTextBlock:
			if found := findText(segment.Segments(), text); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestEveryCardStyleHasADesignAndAName(t *testing.T) {
	for _, style := range config.CardStyles {
		if _, ok := cardDesigns[style]; !ok {
			t.Errorf("style %q has no design", style)
		}
		if cardStyleLabels[style] == "" {
			t.Errorf("style %q has no name in settings", style)
		}
	}
}

func TestTablesTakeTheCardsTextSizeAndColours(t *testing.T) {
	card, app := newCard(t)
	variant := theme.VariantLight
	app.Settings().SetTheme(newAppTheme(&variant))
	card.SetTextSize(config.TextSizeLarger)
	answered(card, "Q", "| City | Country |\n| --- | --- |\n| Paris | France |")

	var table *cardTable
	for _, segment := range card.content.Segments {
		if found, ok := segment.(*cardTable); ok {
			table = found
		}
	}
	if table == nil {
		t.Fatal("the table was not drawn in the card's theme")
	}

	reading := card.readingTheme()
	texts := test.LaidOutObjects(table.Visual())
	var cells int
	for _, object := range texts {
		text, ok := object.(*canvas.Text)
		if !ok {
			continue
		}
		cells++
		if text.TextSize != reading.Size(theme.SizeNameText) {
			t.Errorf("%q is %v points, want the card's %v", text.Text, text.TextSize, reading.Size(theme.SizeNameText))
		}
		if want := reading.Color(theme.ColorNameForeground, variant); text.Color != want {
			t.Errorf("%q is drawn in %v, want the dark card's %v", text.Text, text.Color, want)
		}
	}
	if cells != 4 {
		t.Fatalf("drew %d cells, want the header and one row of two", cells)
	}
}

func TestAnUnknownTextSizeReadsAtTheDefaultSize(t *testing.T) {
	card, _ := newCard(t)
	card.SetTextSize("huge")
	if card.textScale != 1 {
		t.Fatalf("an unknown text size scales text by %v", card.textScale)
	}
}

func TestAMessageIsCapitalisedAndAnEmptyOneStaysEmpty(t *testing.T) {
	if got := sentence("the API key was refused"); got != "The API key was refused" {
		t.Errorf("got %q", got)
	}
	for _, kept := range []string{"", "ctrl+alt+space: already in use", "macOS refused it", "openai: rate limited"} {
		if got := sentence(kept); got != kept {
			t.Errorf("%q became %q", kept, got)
		}
	}
}

func TestATableKeepsItsAlignmentLinksAndEmptyRows(t *testing.T) {
	card, _ := newCard(t)
	answered(card, "Q", "| Item | Price |\n| --- | ---: |\n| [Docs](https://example.com) | 4 |\n|  |  |")

	var table *cardTable
	for _, segment := range card.content.Segments {
		if found, ok := segment.(*cardTable); ok {
			table = found
		}
	}
	if table == nil {
		t.Fatal("no table in the answer")
	}
	if table.align(1) != fyne.TextAlignTrailing {
		t.Fatalf("the price column aligns %v, want trailing", table.align(1))
	}

	objects := test.LaidOutObjects(table.Visual())
	var link bool
	for _, object := range objects {
		if _, ok := object.(*widget.Hyperlink); ok {
			link = true
		}
	}
	if !link {
		t.Error("the link in a cell lost its hyperlink")
	}

	grid := table.Visual().(*container.Scroll).Content.(*fyne.Container)
	layout := grid.Layout.(*tableLayout)
	_, heights := layout.measure(grid.Objects)
	if empty := heights[len(heights)-1]; empty < heights[0] {
		t.Errorf("an empty row is %v high, want as tall as the header (%v)", empty, heights[0])
	}
}

func TestTheCardShowsWhatIsLookedUpUntilTheAnswerStarts(t *testing.T) {
	card, _ := newCard(t)
	update, _, status := card.Open("Weather in Paris?", func() {})

	status("Checking the weather in Paris\nLooking up “Paris” on Wikipedia")
	got := card.content.String()
	for _, line := range []string{"Checking the weather in Paris…", "Looking up “Paris” on Wikipedia…"} {
		if !strings.Contains(got, line) {
			t.Errorf("the card lacks %q:\n%s", line, got)
		}
	}
	if strings.Contains(got, "Thinking") {
		t.Error("the card says it is thinking while it looks things up")
	}

	update("Sunny, 21°C.", true)
	if got := card.content.String(); strings.Contains(got, "Checking the weather") || !strings.Contains(got, "Sunny") {
		t.Fatalf("after the answer the card reads %q", got)
	}
}

func TestTheLookupLinesGoWhenTheAnswerEndsWithoutText(t *testing.T) {
	card, _ := newCard(t)
	update, _, status := card.Open("q", func() {})
	status("Searching the web for “go”")
	update("", true)
	if got := card.content.String(); strings.Contains(got, "Searching") {
		t.Fatalf("a finished card still reads %q", got)
	}

	_, fail, status := card.Open("q", func() {})
	status("Searching the web for “go”")
	fail("the service is having trouble")
	if got := card.content.String(); strings.Contains(got, "Searching") || !strings.Contains(got, "The service is having trouble") {
		t.Fatalf("a failed card reads %q", got)
	}
}

func TestTheInputIsReadyOnceAnAnswerIsIn(t *testing.T) {
	card, _ := newCard(t)
	update, _, _ := card.Open("spoken question", func() {})
	update("Half an ans", false)
	if card.window.Canvas().Focused() == card.input {
		t.Fatal("the input took the focus before the answer was in")
	}
	update("Half an answer.", true)
	if card.window.Canvas().Focused() != card.input {
		t.Fatal("a finished answer leaves the input without the cursor")
	}

	card.window.Canvas().Unfocus()
	_, fail, _ := card.Open("another", func() {})
	fail("the service is having trouble")
	if card.window.Canvas().Focused() != card.input {
		t.Fatal("a failed answer leaves the input without the cursor")
	}
}
