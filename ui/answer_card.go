package ui

import (
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/overlay"
)

const (
	answerWidth     = 480
	answerMaxHeight = 440
	answerInset     = 12
	answerRadius    = 14
	maxInputRows    = 4
	copiedFor       = 1500 * time.Millisecond
	renderEvery     = 50 * time.Millisecond
)

// AnswerCard shows answers where the indicator was, as they are written, and takes typed questions.
// Unlike the indicator it takes the keyboard, so Esc can close it.
type AnswerCard struct {
	app     fyne.App
	window  fyne.Window
	visible bool
	typing  bool
	onAsk   func(question string)

	sessions atomic.Uint64
	session  uint64
	question string
	text     string
	failure  string
	done     bool
	stop     func()

	spot     overlay.Spot
	placed   bool
	size     fyne.Size
	rendered time.Time
	pending  bool
	later    func(time.Duration, func())

	content *widget.RichText
	scroll  *container.Scroll
	footer  fyne.CanvasObject
	input   *questionEntry
	rows    int
	send    *widget.Button
	copy    *widget.Button

	onShow func()
	onHide func()
}

func NewAnswerCard(app fyne.App) *AnswerCard {
	return &AnswerCard{
		app: app,
		later: func(wait time.Duration, run func()) {
			time.AfterFunc(wait, func() { fyne.Do(run) })
		},
	}
}

func (c *AnswerCard) SetShowHideCallbacks(onShow, onHide func()) {
	c.onShow = onShow
	c.onHide = onHide
}

// SetOnAsk receives each question typed into the card, on the UI thread.
func (c *AnswerCard) SetOnAsk(onAsk func(question string)) {
	c.onAsk = onAsk
}

// Prompt opens the card with the keyboard in its input. It may be called from any goroutine.
func (c *AnswerCard) Prompt() {
	fyne.Do(c.prompt)
}

// Open shows question and returns what fills in its answer: update with the text so far, then a done
// update or fail. Closing the card calls stop. All of them may be called from any goroutine; once a
// newer question is shown, the older one's are ignored.
func (c *AnswerCard) Open(question string, stop func()) (update func(text string, done bool), fail func(reason string)) {
	id := c.sessions.Add(1)
	fyne.Do(func() { c.open(id, question, stop) })

	update = func(text string, done bool) {
		fyne.Do(func() {
			if c.session == id {
				c.update(text, done)
			}
		})
	}
	fail = func(reason string) {
		fyne.Do(func() {
			if c.session == id {
				c.fail(reason)
			}
		})
	}
	return update, fail
}

func (c *AnswerCard) prompt() {
	if c.window == nil {
		c.build()
	}
	if !c.visible {
		c.session = c.sessions.Add(1)
		c.question, c.text, c.failure, c.done, c.stop = "", "", "", true, nil
	}
	c.typing = true
	c.show()
	c.window.Canvas().Focus(c.input)
}

func (c *AnswerCard) open(id uint64, question string, stop func()) {
	if c.window == nil {
		c.build()
	}
	if !c.done && c.stop != nil {
		c.stop()
	}
	// A spoken question is answered without the input; Prompt brings it in for a follow-up.
	if !c.visible {
		c.typing = false
	}
	c.session = id
	c.question, c.text, c.failure, c.done, c.stop = question, "", "", false, stop
	c.scroll.ScrollToTop()
	c.show()
}

func (c *AnswerCard) show() {
	if !c.visible {
		c.spot, c.placed = c.findSpot()
		c.size = fyne.Size{}
	}
	c.render()

	if !c.visible && c.onShow != nil {
		c.onShow()
	}
	c.visible = true
	// The first Show creates the native window, which the placement in render could not reach yet.
	c.window.Show()
	c.float()
	c.window.RequestFocus()
}

func (c *AnswerCard) update(text string, done bool) {
	if !c.visible {
		return
	}
	c.text, c.done = text, done

	wait := renderEvery - time.Since(c.rendered)
	if done || wait <= 0 {
		c.render()
		return
	}
	if !c.pending {
		c.pending = true
		c.later(wait, func() {
			c.pending = false
			if c.visible {
				c.render()
			}
		})
	}
}

func (c *AnswerCard) fail(reason string) {
	if !c.visible {
		return
	}
	c.failure, c.done = reason, true
	c.render()
}

// render keeps the end of the answer in view, unless the reader has scrolled away from it.
func (c *AnswerCard) render() {
	following := c.scroll.Offset.Y >= c.content.Size().Height-c.scroll.Size().Height-1

	c.content.Segments = c.segments()
	c.content.Refresh()
	if len(c.content.Segments) == 0 {
		c.scroll.Hide()
	} else {
		c.scroll.Show()
	}
	setVisible(c.copy, c.text != "")
	setVisible(c.input, c.typing)
	setVisible(c.send, c.typing)

	if size := c.fit(); size != c.size {
		c.size = size
		c.window.Resize(size)
		c.float()
	}
	if following {
		c.scroll.ScrollToBottom()
	}
	c.rendered = time.Now()
}

func (c *AnswerCard) segments() []widget.RichTextSegment {
	var segments []widget.RichTextSegment
	if c.question != "" {
		segments = append(segments,
			&widget.TextSegment{Text: c.question, Style: mutedStyle()},
			&widget.SeparatorSegment{},
		)
	}
	switch {
	case c.text != "":
		segments = append(segments, widget.NewRichTextFromMarkdown(c.text).Segments...)
	case !c.done && c.question != "":
		segments = append(segments, &widget.TextSegment{Text: "Thinking…", Style: mutedStyle()})
	}
	if c.failure != "" {
		segments = append(segments, &widget.TextSegment{
			Text:  c.failure,
			Style: widget.RichTextStyle{ColorName: theme.ColorNameError},
		})
	}
	return segments
}

func (c *AnswerCard) Hide() {
	if !c.visible {
		return
	}
	c.visible = false
	if !c.done && c.stop != nil {
		c.stop()
	}
	c.stop = nil
	c.window.Hide()
	if c.onHide != nil {
		c.onHide()
	}
}

func (c *AnswerCard) Visible() bool { return c.visible }

func (c *AnswerCard) submit() {
	question := strings.TrimSpace(c.input.Text)
	if question == "" || c.onAsk == nil {
		return
	}
	c.input.SetText("")
	c.onAsk(question)
}

func (c *AnswerCard) build() {
	c.window = borderlessWindow(c.app)
	c.window.SetTitle("Encre")

	c.content = widget.NewRichText()
	c.content.Wrapping = fyne.TextWrapWord
	c.scroll = container.NewVScroll(c.content)

	c.input = newQuestionEntry(c.submit, c.Hide)
	c.input.SetPlaceHolder("Ask anything")
	c.input.OnChanged = c.inputChanged
	c.rows = 1
	c.send = iconButton(theme.MailSendIcon(), c.submit)
	c.copy = iconButton(theme.ContentCopyIcon(), c.copyAnswer)
	actions := container.NewHBox(c.send, c.copy, iconButton(theme.CancelIcon(), c.Hide))
	c.footer = container.NewBorder(nil, nil, nil, actions, c.input)

	body := container.New(&answerLayout{scroll: c.scroll, footer: c.footer}, c.scroll, c.footer)
	inset := container.New(layout.NewCustomPaddedLayout(answerInset, answerInset, answerInset, answerInset), body)
	c.window.SetContent(container.NewStack(
		canvas.NewRectangle(overlay.Surface),
		container.NewThemeOverride(inset, &fixedVariant{theme.VariantDark}),
	))

	c.window.SetCloseIntercept(c.Hide)
	c.window.Canvas().SetOnTypedKey(func(event *fyne.KeyEvent) {
		if event.Name == fyne.KeyEscape {
			c.Hide()
		}
	})
	c.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { c.copyAnswer() })
}

// fit is the layout's own minimum, grown by whatever the answer needs beyond the scroll's, up to the cap.
func (c *AnswerCard) fit() fyne.Size {
	height := c.window.Content().MinSize().Height
	if c.scroll.Visible() {
		c.content.Resize(fyne.NewSize(answerWidth-2*answerInset, 0))
		height += max(c.content.MinSize().Height-c.scroll.MinSize().Height, 0)
	}
	return fyne.NewSize(answerWidth, min(height, answerMaxHeight))
}

func (c *AnswerCard) inputChanged(text string) {
	rows := min(max(c.input.rows(text), 1), maxInputRows)
	if rows == c.rows {
		return
	}
	c.rows = rows
	c.input.SetMinRowsVisible(rows)
	c.render()
}

func (c *AnswerCard) copyAnswer() {
	c.app.Clipboard().SetContent(c.text)
	c.copy.SetIcon(theme.ConfirmIcon())

	c.later(copiedFor, func() { c.copy.SetIcon(theme.ContentCopyIcon()) })
}

func (c *AnswerCard) findSpot() (overlay.Spot, bool) {
	if _, ok := c.window.(driver.NativeWindow); !ok {
		return overlay.Spot{}, false
	}
	return overlay.FindSpot()
}

func (c *AnswerCard) float() {
	if !c.placed {
		return
	}
	native := c.window.(driver.NativeWindow)

	scale := c.window.Canvas().Scale()
	frame := c.spot.Frame(int(c.size.Width*scale), int(c.size.Height*scale))
	radius := int(answerRadius * scale)

	native.RunNative(func(context any) {
		switch window := context.(type) {
		case driver.X11WindowContext:
			overlay.Panel(window.WindowHandle, frame, radius)
		case driver.WindowsWindowContext:
			overlay.Panel(window.HWND, frame, radius)
		case driver.MacWindowContext:
			overlay.Panel(window.NSWindow, frame, radius)
		}
	})
}

func mutedStyle() widget.RichTextStyle {
	return widget.RichTextStyle{
		ColorName: theme.ColorNamePlaceHolder,
		TextStyle: fyne.TextStyle{Italic: true},
	}
}

// answerLayout puts the footer at the bottom and gives the scroll the rest, with a gap between them only
// while the scroll shows: an empty card has the same space above its input as below.
type answerLayout struct {
	scroll fyne.CanvasObject
	footer fyne.CanvasObject
}

func (l *answerLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	size := l.footer.MinSize()
	if l.scroll.Visible() {
		size.Height += l.scroll.MinSize().Height + theme.Padding()
	}
	return size
}

func (l *answerLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	footer := l.footer.MinSize().Height
	l.footer.Resize(fyne.NewSize(size.Width, footer))
	l.footer.Move(fyne.NewPos(0, size.Height-footer))
	l.scroll.Resize(fyne.NewSize(size.Width, size.Height-footer-theme.Padding()))
	l.scroll.Move(fyne.NewPos(0, 0))
}

// questionEntry sends on Enter and breaks the line on Shift+Enter, as chat inputs do.
type questionEntry struct {
	widget.Entry
	shift  bool
	send   func()
	cancel func()
}

func newQuestionEntry(send, cancel func()) *questionEntry {
	entry := &questionEntry{send: send, cancel: cancel}
	entry.MultiLine = true
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(1)
	entry.ExtendBaseWidget(entry)
	return entry
}

func (e *questionEntry) KeyDown(key *fyne.KeyEvent) {
	if isShift(key) {
		e.shift = true
	}
	e.Entry.KeyDown(key)
}

func (e *questionEntry) KeyUp(key *fyne.KeyEvent) {
	if isShift(key) {
		e.shift = false
	}
	e.Entry.KeyUp(key)
}

func (e *questionEntry) TypedKey(key *fyne.KeyEvent) {
	switch {
	case key.Name == fyne.KeyEscape:
		e.cancel()
	case (key.Name == fyne.KeyReturn || key.Name == fyne.KeyEnter) && !e.shift:
		e.send()
	default:
		e.Entry.TypedKey(key)
	}
}

// rows is how many lines text wraps to at the entry's width, breaking between words as the entry does.
func (e *questionEntry) rows(text string) int {
	width := e.Size().Width - 2*(theme.InnerPadding()+theme.Padding())
	if width <= 0 {
		return 1
	}
	fits := func(line string) bool {
		return fyne.MeasureText(line, theme.TextSize(), e.TextStyle).Width <= width
	}

	rows := 0
	for _, paragraph := range strings.Split(text, "\n") {
		rows++
		line := ""
		for _, word := range strings.Fields(paragraph) {
			if line != "" && !fits(line+" "+word) {
				rows++
				line = word
				continue
			}
			line = strings.TrimPrefix(line+" "+word, " ")
		}
	}
	return rows
}

func isShift(key *fyne.KeyEvent) bool {
	return key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight
}

func setVisible(object fyne.CanvasObject, visible bool) {
	if visible {
		object.Show()
	} else {
		object.Hide()
	}
}

func iconButton(icon fyne.Resource, tapped func()) *widget.Button {
	button := widget.NewButtonWithIcon("", icon, tapped)
	button.Importance = widget.LowImportance
	return button
}

func borderlessWindow(app fyne.App) fyne.Window {
	drv, ok := app.Driver().(desktop.Driver)
	if !ok {
		return app.NewWindow("Encre")
	}
	window := drv.CreateSplashWindow()
	if floating, ok := window.(desktop.Window); ok {
		floating.RequestAlwaysOnTop()
	}
	return window
}
