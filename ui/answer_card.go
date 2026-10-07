package ui

import (
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
	answerMinHeight = 96
	answerMaxHeight = 440
	answerInset     = 12
	answerRadius    = 14
	copiedFor       = 1500 * time.Millisecond
	renderEvery     = 50 * time.Millisecond
)

// AnswerCard shows an answer where the indicator was, as it is written. Unlike the indicator it takes
// the keyboard, so Esc can close it. Open and Update may be called from any goroutine.
type AnswerCard struct {
	app     fyne.App
	window  fyne.Window
	visible bool

	question string
	text     string
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

// Open shows the card for a new question; stop is called if it is closed before the answer is done.
func (c *AnswerCard) Open(question string, stop func()) {
	fyne.Do(func() { c.open(question, stop) })
}

// Update replaces the answer written so far; done marks it complete.
func (c *AnswerCard) Update(text string, done bool) {
	fyne.Do(func() { c.update(text, done) })
}

func (c *AnswerCard) open(question string, stop func()) {
	if c.window == nil {
		c.build()
	}
	c.spot, c.placed = c.findSpot()
	c.question, c.text, c.done, c.stop = question, "", false, stop
	c.size = fyne.Size{}
	c.copy.SetIcon(theme.ContentCopyIcon())
	c.scroll.ScrollToTop()
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

// render keeps the end of the answer in view, unless the reader has scrolled away from it.
func (c *AnswerCard) render() {
	following := c.scroll.Offset.Y >= c.content.Size().Height-c.scroll.Size().Height-1

	c.content.Segments = append(questionSegments(c.question), widget.NewRichTextFromMarkdown(c.text).Segments...)
	c.content.Refresh()
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

func (c *AnswerCard) Hide() {
	if !c.visible {
		return
	}
	c.visible = false
	if !c.done && c.stop != nil {
		c.stop()
	}
	c.window.Hide()
	if c.onHide != nil {
		c.onHide()
	}
}

func (c *AnswerCard) Visible() bool { return c.visible }

func (c *AnswerCard) build() {
	c.window = borderlessWindow(c.app)
	c.window.SetTitle("Encre")

	c.content = widget.NewRichText()
	c.content.Wrapping = fyne.TextWrapWord
	c.scroll = container.NewVScroll(c.content)

	c.copy = iconButton(theme.ContentCopyIcon(), c.copyAnswer)
	hint := widget.NewRichText(&widget.TextSegment{Text: "Esc to close", Style: mutedStyle(true)})
	c.footer = container.NewBorder(nil, nil, hint, container.NewHBox(c.copy, iconButton(theme.CancelIcon(), c.Hide)))

	body := container.NewBorder(nil, c.footer, nil, nil, c.scroll)
	inset := container.New(layout.NewCustomPaddedLayout(answerInset, answerInset/2, answerInset, answerInset/2), body)
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

func (c *AnswerCard) fit() fyne.Size {
	c.content.Resize(fyne.NewSize(answerWidth-answerInset*3/2, 0))

	height := c.content.MinSize().Height + c.footer.MinSize().Height + answerInset*3/2 + theme.Padding()
	return fyne.NewSize(answerWidth, min(max(height, answerMinHeight), answerMaxHeight))
}

func (c *AnswerCard) copyAnswer() {
	c.app.Clipboard().SetContent(c.text)
	c.copy.SetIcon(theme.ConfirmIcon())

	time.AfterFunc(copiedFor, func() {
		fyne.Do(func() { c.copy.SetIcon(theme.ContentCopyIcon()) })
	})
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

func questionSegments(question string) []widget.RichTextSegment {
	return []widget.RichTextSegment{
		&widget.TextSegment{Text: question, Style: mutedStyle(false)},
		&widget.SeparatorSegment{},
	}
}

func mutedStyle(inline bool) widget.RichTextStyle {
	return widget.RichTextStyle{
		ColorName: theme.ColorNamePlaceHolder,
		Inline:    inline,
		TextStyle: fyne.TextStyle{Italic: true},
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
