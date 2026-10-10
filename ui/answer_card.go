package ui

import (
	"image"
	"math"
	"slices"
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
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/history"
	"github.com/paradoxe35/encre/internal/overlay"
)

const (
	answerWidth     = 480
	maxAnswerWidth  = 640
	answerMaxHeight = 440
	answerInset     = 12
	answerRadius    = 14
	maxInputRows    = 4
	copiedFor       = 1500 * time.Millisecond
	renderEvery     = 50 * time.Millisecond
	resizeFor       = 140 * time.Millisecond
	// shrinkFor is longer: a card giving up most of its height reads as a jump at the speed it grows.
	shrinkFor = 240 * time.Millisecond
	fadeFor   = 120 * time.Millisecond
)

// AnswerCard takes the keyboard, unlike the indicator, so Esc can close it.
type AnswerCard struct {
	app     fyne.App
	window  fyne.Window
	visible bool
	onAsk   func(question string)
	asked   func() []history.Entry

	// wanted becomes style once an open card closes, as a new style needs a new window.
	style  config.CardStyle
	wanted config.CardStyle
	// created: a see-through window must be made transparent from the start.
	created  bool
	restyle  bool
	look     *container.ThemeOverride
	backdrop overlay.Backdrop
	fill     *themedFill
	// behind is where frosted was captured, in screen pixels.
	frosted *canvas.Image
	behind  image.Rectangle
	// mapped: the native window is on screen, where a capture would catch it.
	mapped  bool
	opacity float64
	fading  *fyne.Animation
	fade    func(from, to float64, apply func(float64), done func()) *fyne.Animation

	sessions atomic.Uint64
	session  uint64
	// exchange is the session's own; shown, the stored one on the card; following, what a new question continues.
	exchange  string
	shown     string
	following atomic.Pointer[string]
	question  string
	text      string
	failure   string
	status    string
	done      bool
	stop      func()

	spot     overlay.Spot
	placed   bool
	size     fyne.Size
	target   fyne.Size
	resizing *fyne.Animation
	animate  func(from, to fyne.Size, apply func(fyne.Size)) *fyne.Animation
	rendered time.Time
	pending  bool
	later    func(time.Duration, func())

	content   *widget.RichText
	scroll    *container.Scroll
	reading   *container.ThemeOverride
	textScale float32
	footer    fyne.CanvasObject
	input     *questionEntry
	rows      int
	send      *widget.Button
	copy      *widget.Button

	onShow   func()
	onHide   func()
	onClosed func()
}

func NewAnswerCard(app fyne.App) *AnswerCard {
	c := &AnswerCard{
		app:       app,
		textScale: 1,
		later: func(wait time.Duration, run func()) {
			time.AfterFunc(wait, func() { fyne.Do(run) })
		},
		animate: glide,
		fade:    fadeWindow,
	}
	app.Settings().AddListener(func(fyne.Settings) { fyne.Do(c.retheme) })
	return c
}

func (c *AnswerCard) SetShowHideCallbacks(onShow, onHide func()) {
	c.onShow = onShow
	c.onHide = onHide
}

// SetOnClosed runs once the card's window is gone, after it fades out.
func (c *AnswerCard) SetOnClosed(onClosed func()) {
	c.onClosed = onClosed
}

// SetOnAsk's callback runs on the UI thread.
func (c *AnswerCard) SetOnAsk(onAsk func(question string)) {
	c.onAsk = onAsk
}

// SetHistory gives the asked questions, newest first, for the arrow keys to go through.
func (c *AnswerCard) SetHistory(asked func() []history.Entry) {
	c.asked = asked
}

// Following may be called from any goroutine.
func (c *AnswerCard) Following() string {
	if id := c.following.Load(); id != nil {
		return *id
	}
	return ""
}

func (c *AnswerCard) follow(id string) {
	c.following.Store(&id)
}

// Prompt may be called from any goroutine.
func (c *AnswerCard) Prompt() {
	fyne.Do(c.prompt)
}

// Closing the card calls stop; callbacks are goroutine-safe and ignored once a newer question shows.
func (c *AnswerCard) Open(question, exchange string, stop func()) (update func(text string, done bool), fail func(reason string), status func(line string)) {
	id := c.sessions.Add(1)
	fyne.Do(func() { c.open(id, question, exchange, stop) })

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
	status = func(line string) {
		fyne.Do(func() {
			if c.session == id && c.visible {
				c.status = line
				c.render()
			}
		})
	}
	return update, fail, status
}

func (c *AnswerCard) prompt() {
	if c.window == nil {
		c.build()
	}
	if !c.visible {
		c.clear()
	}
	c.show()
	c.focusInput()
}

func (c *AnswerCard) clear() {
	c.session = c.sessions.Add(1)
	c.question, c.text, c.failure, c.status, c.done, c.stop = "", "", "", "", true, nil
	c.exchange, c.shown = "", ""
	c.follow("")
}

func (c *AnswerCard) open(id uint64, question, exchange string, stop func()) {
	if c.window == nil {
		c.build()
	}
	if !c.done && c.stop != nil {
		c.stop()
	}
	c.session = id
	c.question, c.text, c.failure, c.status, c.done, c.stop = question, "", "", "", false, stop
	c.exchange, c.shown = exchange, ""
	c.scroll.ScrollToTop()
	c.show()
}

func (c *AnswerCard) show() {
	appearing := !c.visible
	if appearing {
		c.spot, c.placed = c.findSpot()
		c.size, c.target = fyne.Size{}, fyne.Size{}
	}
	c.render()

	if appearing && c.onShow != nil {
		c.onShow()
	}
	c.visible = true
	// A card still fading out keeps its capture: the screen behind it would show the card itself.
	if appearing && !c.hideFading() && c.frosted != nil {
		c.frost()
	}
	// The first Show creates the native window, which the placement in render could not reach yet.
	c.showWindow()
	if appearing {
		c.opacity = 0
		c.setOpacity(0)
	}
	c.float()
	c.window.RequestFocus()
	if appearing {
		c.fadeTo(1, nil)
	}
}

// hideFading reports whether it took down a card still fading out.
func (c *AnswerCard) hideFading() bool {
	if !c.mapped {
		return false
	}
	if c.fading != nil {
		c.fading.Stop()
		c.fading = nil
	}
	c.setOpacity(0)
	c.window.Hide()
	c.mapped = false
	return true
}

func (c *AnswerCard) showWindow() {
	if !c.created && c.seeThrough() {
		overlay.Transparent(c.window.Show)
	} else {
		c.window.Show()
	}
	c.created, c.mapped = true, true
}

// seeThrough is glass the desktop composes, rather than glass drawn over a frosted capture.
func (c *AnswerCard) seeThrough() bool {
	return designFor(c.style).glass && (c.backdrop == overlay.BackdropBlurred || c.backdrop == overlay.BackdropSharp)
}

// frost allows for scale 2: before its first show the window may not know its screen's scale.
func (c *AnswerCard) frost() {
	c.fill.fill = colorNameCard
	c.frosted.Hide()
	if c.placed {
		scale := c.window.Canvas().Scale()
		if !c.created {
			scale = max(scale, 2)
		}
		area := c.spot.Frame(int(maxAnswerWidth*scale), int(answerMaxHeight*scale))
		if shot, behind, ok := overlay.Frosted(area); ok {
			c.frosted.Image, c.behind = shot, behind
			c.fill.fill = colorNameGlass
			c.frosted.Show()
			c.frosted.Refresh()
		}
	}
	c.fill.Refresh()
}

func (c *AnswerCard) update(text string, done bool) {
	if !c.visible {
		return
	}
	c.text, c.done = text, done
	if text != "" || done {
		c.status = ""
	}

	wait := renderEvery - time.Since(c.rendered)
	if done || wait <= 0 {
		c.render()
		if done {
			c.shown = c.exchange
			c.follow(c.exchange)
			c.focusInput()
		}
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
	c.failure, c.status, c.done = sentence(reason), "", true
	c.render()
	c.focusInput()
}

// focusInput moves the focus within the card only, so an app the user went back to keeps the keyboard.
func (c *AnswerCard) focusInput() {
	c.window.Canvas().Focus(c.input)
}

// render never scrolls: text arriving past the card's bottom waits there.
func (c *AnswerCard) render() {
	c.content.Segments = c.segments()
	c.content.Refresh()
	if len(c.content.Segments) == 0 {
		c.reading.Hide()
	} else {
		c.reading.Show()
	}
	setVisible(c.copy, c.text != "")

	if size := c.fit(); size != c.target {
		c.resizeTo(size)
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
		segments = append(segments, adopt(c.content, widget.NewRichTextFromMarkdown(c.text).Segments)...)
	case c.status != "":
		for _, line := range strings.Split(c.status, "\n") {
			segments = append(segments, &widget.TextSegment{Text: line + "…", Style: mutedStyle()})
		}
	case !c.done && c.question != "":
		segments = append(segments, &widget.TextSegment{Text: "Thinking…", Style: mutedStyle()})
	}
	if c.failure != "" {
		segments = append(segments, &widget.TextSegment{
			Text:  c.failure,
			Style: widget.RichTextStyle{ColorName: theme.ColorNameError},
		})
	}
	if designFor(c.style).monospace {
		eachText(segments, func(text *widget.TextSegment) { text.Style.TextStyle.Monospace = true })
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
	c.follow("")
	if c.onHide != nil {
		c.onHide()
	}
	c.fadeTo(0, func() {
		if c.visible {
			return
		}
		c.window.Hide()
		c.mapped = false
		if c.restyle {
			c.discard()
		}
		if c.onClosed != nil {
			c.onClosed()
		}
	})
}

// done runs only if the fade finishes.
func (c *AnswerCard) fadeTo(opacity float64, done func()) {
	if c.fading != nil {
		c.fading.Stop()
	}
	c.fading = c.fade(c.opacity, opacity, func(o float64) {
		c.opacity = o
		c.setOpacity(o)
	}, done)
}

func fadeWindow(from, to float64, apply func(float64), done func()) *fyne.Animation {
	animation := fyne.NewAnimation(fadeFor, func(progress float32) {
		apply(from + (to-from)*float64(progress))
		if progress == 1 && done != nil {
			done()
		}
	})
	animation.Start()
	return animation
}

// SetStyle may be called from any goroutine.
func (c *AnswerCard) SetStyle(style config.CardStyle) {
	fyne.Do(func() {
		c.wanted = style
		switch {
		case style == c.style:
			c.restyle = false
		case c.window == nil:
			c.style = style
		case c.visible:
			c.restyle = true
		default:
			c.discard()
		}
	})
}

// discard drops the window, so the next one is made in the current style.
func (c *AnswerCard) discard() {
	for _, animation := range []*fyne.Animation{c.resizing, c.fading} {
		if animation != nil {
			animation.Stop()
		}
	}
	c.window.Close()
	c.window, c.created, c.restyle, c.mapped = nil, false, false, false
	c.frosted, c.fill = nil, nil
	c.style = c.wanted
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
	c.reading = container.NewThemeOverride(c.scroll, c.readingTheme())

	c.input = newQuestionEntry(c.submit, c.Hide, c.browse)
	c.input.TextStyle.Monospace = designFor(c.style).monospace
	c.input.SetPlaceHolder("Ask anything")
	c.input.OnChanged = c.inputChanged
	c.rows = 1
	c.send = iconButton(theme.MailSendIcon(), c.submit)
	c.copy = iconButton(theme.ContentCopyIcon(), c.copyAnswer)
	actions := container.NewHBox(c.send, c.copy, iconButton(theme.CancelIcon(), c.Hide))
	c.footer = container.NewBorder(nil, nil, nil, actions, c.input)

	body := container.New(&answerLayout{scroll: c.reading, footer: c.footer}, c.reading, c.footer)
	inset := container.New(layout.NewCustomPaddedLayout(answerInset, answerInset, answerInset, answerInset), body)
	c.look = container.NewThemeOverride(container.NewStack(c.surface(), inset), c.cardTheme())
	c.window.SetContent(container.New(&sizeWatch{onSize: c.laidOut}, newWheelCatch(c.look, c.scroll)))

	c.window.SetCloseIntercept(c.Hide)
	c.window.Canvas().SetOnTypedKey(func(event *fyne.KeyEvent) {
		if event.Name == fyne.KeyEscape {
			c.Hide()
		}
	})
	c.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { c.copyAnswer() })
}

// browse steps to an older exchange, or a newer one and past the newest to a fresh card, which a new question then continues.
func (c *AnswerCard) browse(older bool) {
	if !c.done || c.asked == nil {
		return
	}
	asked := c.asked()
	at := -1
	if c.shown != "" {
		at = slices.IndexFunc(asked, func(entry history.Entry) bool { return entry.ID == c.shown })
	}
	if older {
		at++
	} else {
		at--
	}
	switch {
	case at < -1 || at >= len(asked):
		return
	case at == -1:
		c.clear()
	default:
		c.clear()
		c.question, c.text, c.shown = asked[at].Original, asked[at].Result, asked[at].ID
		c.follow(c.shown)
	}
	c.scroll.ScrollToTop()
	c.render()
}

// fit is the layout's own minimum, grown by whatever the answer needs beyond the scroll's, up to the cap.
func (c *AnswerCard) fit() fyne.Size {
	height := c.window.Content().MinSize().Height
	width := c.width()
	if c.reading.Visible() {
		// Re-lay the scroll after measuring, or it ignores the wheel; then restore the reader's place.
		reading := c.scroll.Offset
		c.content.Resize(fyne.NewSize(width-2*answerInset, 0))
		height += max(c.content.MinSize().Height-c.scroll.MinSize().Height, 0)
		c.scroll.Refresh()
		c.scroll.ScrollToOffset(reading)
	}
	return fyne.NewSize(width, min(height, answerMaxHeight))
}

// width widens the card with larger text, so a line keeps a comfortable number of words.
func (c *AnswerCard) width() float32 {
	return min(answerWidth*max(c.textScale, 1), maxAnswerWidth)
}

// SetTextSize may be called from any goroutine.
func (c *AnswerCard) SetTextSize(size config.TextSize) {
	fyne.Do(func() {
		scale, ok := textScales[size]
		if !ok {
			scale = textScales[config.TextSizeDefault]
		}
		c.textScale = scale
		c.retheme()
	})
}

func (c *AnswerCard) retheme() {
	if c.window == nil {
		return
	}
	c.look.Theme = c.cardTheme()
	c.reading.Theme = c.readingTheme()
	c.look.Refresh()
	if c.visible {
		c.render()
	}
}

func (c *AnswerCard) cardTheme() fyne.Theme {
	app, ok := c.app.Settings().Theme().(*appTheme)
	if !ok {
		app = newAppTheme(nil)
	}
	return newCardTheme(app, designFor(c.style))
}

func (c *AnswerCard) readingTheme() fyne.Theme {
	return &scaledText{Theme: c.cardTheme(), scale: c.textScale}
}

// Only see-through glass is drawn rounded; other cards have their corners cut from the window.
func (c *AnswerCard) surface() fyne.CanvasObject {
	design := designFor(c.style)
	c.backdrop, c.frosted = overlay.BackdropNone, nil
	if design.glass && c.native() {
		c.backdrop = overlay.GlassBackdrop()
	}

	var layers []fyne.CanvasObject
	if design.glow != nil {
		layers = design.glow()
	}
	fill, radius := colorNameCard, float32(0)
	switch {
	case !design.glass:
	case c.backdrop == overlay.BackdropBlurred:
		fill, radius = colorNameGlass, c.corner()
	case c.backdrop == overlay.BackdropSharp:
		fill, radius = colorNameFrost, c.corner()
	case c.backdrop == overlay.BackdropFrosted:
		c.frosted = &canvas.Image{FillMode: canvas.ImageFillStretch, ScaleMode: canvas.ImageScaleSmooth}
		c.frosted.Hide()
		layers = append(layers, container.NewWithoutLayout(c.frosted))
	}
	c.fill = newThemedFill(fill, "", radius)
	layers = append(layers, c.fill, newThemedFill("", colorNameCardEdge, c.corner()))
	return container.NewStack(layers...)
}

func (c *AnswerCard) corner() float32 {
	if !c.native() {
		return answerRadius
	}
	return overlay.Corner(answerRadius)
}

func (c *AnswerCard) resizeTo(size fyne.Size) {
	c.target = size
	// A window manager refuses a size below the last minimum it was given, so refit it first.
	c.window.SetFixedSize(false)
	if c.resizing != nil {
		c.resizing.Stop()
		c.resizing = nil
	}
	if !c.visible || c.size.IsZero() {
		c.size = size
		c.window.Resize(size)
		c.float()
		return
	}
	c.resizing = c.animate(c.size, size, c.applySize)
}

// applySize moves and sizes in one step: sized alone, the bottom-anchored card would jump.
func (c *AnswerCard) applySize(size fyne.Size) {
	// Below the content's minimum, Fyne pushes the window back up and the two sizes chase each other forever.
	least := c.window.Content().MinSize()
	c.size = c.wholePixels(fyne.NewSize(max(size.Width, least.Width), max(size.Height, least.Height)))
	c.float()
	c.window.Resize(c.size)
}

// wholePixels rounds up as Fyne does, so they agree and the content never outgrows the card by a fraction.
func (c *AnswerCard) wholePixels(size fyne.Size) fyne.Size {
	scale := float64(c.window.Canvas().Scale())
	up := func(v float32) float32 { return float32(math.Ceil(float64(v)*scale-0.001) / scale) }
	return fyne.NewSize(up(size.Width), up(size.Height))
}

func glide(from, to fyne.Size, apply func(fyne.Size)) *fyne.Animation {
	duration, curve := glideFor(from, to)
	animation := fyne.NewAnimation(duration, func(progress float32) {
		apply(fyne.NewSize(from.Width+(to.Width-from.Width)*progress, from.Height+(to.Height-from.Height)*progress))
	})
	animation.Curve = curve
	animation.Start()
	return animation
}

func glideFor(from, to fyne.Size) (time.Duration, fyne.AnimationCurve) {
	if to.Height < from.Height {
		return shrinkFor, fyne.AnimationEaseInOut
	}
	return resizeFor, fyne.AnimationEaseOut
}

// laidOut follows the size the window was actually given, so the frame always matches what is drawn.
func (c *AnswerCard) laidOut(size fyne.Size) {
	if !c.visible || size == c.size {
		return
	}
	c.size = size
	c.float()
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

func (c *AnswerCard) native() bool {
	_, ok := c.window.(driver.NativeWindow)
	return ok
}

func (c *AnswerCard) findSpot() (overlay.Spot, bool) {
	if !c.native() {
		return overlay.Spot{}, false
	}
	return overlay.FindSpot()
}

func (c *AnswerCard) setOpacity(opacity float64) {
	c.onNative(func(handle uintptr) { overlay.SetOpacity(handle, opacity) })
}

func (c *AnswerCard) onNative(run func(handle uintptr)) {
	native, ok := c.window.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		switch window := context.(type) {
		case driver.X11WindowContext:
			run(window.WindowHandle)
		case driver.WindowsWindowContext:
			run(window.HWND)
		case driver.MacWindowContext:
			run(window.NSWindow)
		}
	})
}

func (c *AnswerCard) float() {
	if !c.placed {
		return
	}
	scale := c.window.Canvas().Scale()
	frame := c.spot.Frame(int(math.Round(float64(c.size.Width*scale))), int(math.Round(float64(c.size.Height*scale))))
	look := overlay.Look{Radius: int(answerRadius * scale), Glass: c.seeThrough()}
	c.onNative(func(handle uintptr) { overlay.Panel(handle, frame, look) })

	// The frosted screen stays where it was taken while the card grows and shrinks over it.
	if c.frosted != nil && c.frosted.Visible() {
		c.frosted.Move(fyne.NewPos(float32(c.behind.Min.X-frame.Min.X)/scale, float32(c.behind.Min.Y-frame.Min.Y)/scale))
		c.frosted.Resize(fyne.NewSize(float32(c.behind.Dx())/scale, float32(c.behind.Dy())/scale))
	}
}

func mutedStyle() widget.RichTextStyle {
	return widget.RichTextStyle{
		ColorName: theme.ColorNamePlaceHolder,
		TextStyle: fyne.TextStyle{Italic: true},
	}
}

// answerLayout gaps footer and scroll only while the scroll shows, so an empty card stays balanced.
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

var textScales = map[config.TextSize]float32{
	config.TextSizeSmall:   0.9,
	config.TextSizeDefault: 1,
	config.TextSizeLarge:   1.15,
	config.TextSizeLarger:  1.3,
}

type scaledText struct {
	fyne.Theme
	scale float32
}

func (t *scaledText) Size(name fyne.ThemeSizeName) float32 {
	size := t.Theme.Size(name)
	switch name {
	case theme.SizeNameText, theme.SizeNameHeadingText, theme.SizeNameSubHeadingText,
		theme.SizeNameCaptionText, theme.SizeNameInlineIcon:
		return size * t.scale
	}
	return size
}

// sizeWatch lays its one child over the whole window and reports the size it was given.
type sizeWatch struct {
	onSize func(fyne.Size)
}

func (w *sizeWatch) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (w *sizeWatch) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(size)
	objects[0].Move(fyne.NewPos(0, 0))
	w.onSize(size)
}

// wheelCatch exists because Fyne aims the wheel at the last pointer position, stale once the card grows.
type wheelCatch struct {
	widget.BaseWidget
	content fyne.CanvasObject
	answer  fyne.Scrollable
}

func newWheelCatch(content fyne.CanvasObject, answer fyne.Scrollable) *wheelCatch {
	catch := &wheelCatch{content: content, answer: answer}
	catch.ExtendBaseWidget(catch)
	return catch
}

func (w *wheelCatch) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

func (w *wheelCatch) Scrolled(event *fyne.ScrollEvent) {
	w.answer.Scrolled(event)
}

// questionEntry sends on Enter and breaks the line on Shift+Enter, as chat inputs do.
type questionEntry struct {
	widget.Entry
	shift  bool
	send   func()
	cancel func()
	browse func(older bool)
}

func newQuestionEntry(send, cancel func(), browse func(older bool)) *questionEntry {
	entry := &questionEntry{send: send, cancel: cancel, browse: browse}
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
	// Only an empty input browses, so the arrows still move through a question being written.
	case (key.Name == fyne.KeyUp || key.Name == fyne.KeyDown) && e.Text == "":
		e.browse(key.Name == fyne.KeyUp)
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
		window := app.NewWindow("Encre")
		window.SetPadded(false)
		return window
	}
	window := drv.CreateSplashWindow()
	if floating, ok := window.(desktop.Window); ok {
		floating.RequestAlwaysOnTop()
	}
	return window
}
