package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/overlay"
)

const (
	answerWidth     = 480
	answerMinHeight = 120
	answerMaxHeight = 440
	copiedFor       = 1500 * time.Millisecond
)

// AnswerCard shows the reply to a spoken question until it is dismissed. Like the indicator it floats
// above other windows and stays out of the taskbar, but it takes the keyboard so Esc can close it.
// Every method runs on the UI thread.
type AnswerCard struct {
	app     fyne.App
	window  fyne.Window
	visible bool
	text    string

	header   fyne.CanvasObject
	question *widget.Label
	answer   *widget.RichText
	scroll   *container.Scroll
	copy     *widget.Button

	onShow func()
	onHide func()
}

func NewAnswerCard(app fyne.App) *AnswerCard {
	return &AnswerCard{app: app}
}

func (c *AnswerCard) SetShowHideCallbacks(onShow, onHide func()) {
	c.onShow = onShow
	c.onHide = onHide
}

// Show replaces whatever the card was showing.
func (c *AnswerCard) Show(question, answer string) {
	if c.window == nil {
		c.build()
	}

	c.text = answer
	c.question.SetText(question)
	c.answer.ParseMarkdown(answer)
	c.copy.SetIcon(theme.ContentCopyIcon())
	c.window.Resize(c.fit())
	c.scroll.ScrollToTop()

	if !c.visible && c.onShow != nil {
		c.onShow()
	}
	c.visible = true
	// The first Show creates the native window, so only the second call reaches it then.
	c.float()
	c.window.Show()
	c.float()
	c.window.RequestFocus()
}

func (c *AnswerCard) Hide() {
	if !c.visible {
		return
	}
	c.visible = false
	c.window.Hide()
	if c.onHide != nil {
		c.onHide()
	}
}

func (c *AnswerCard) Visible() bool { return c.visible }

func (c *AnswerCard) build() {
	c.window = borderlessWindow(c.app)
	c.window.SetTitle("Encre")

	c.question = widget.NewLabel("")
	c.question.Truncation = fyne.TextTruncateEllipsis
	c.question.TextStyle.Italic = true

	c.copy = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), c.copyAnswer)
	c.copy.Importance = widget.LowImportance
	dismiss := widget.NewButtonWithIcon("", theme.CancelIcon(), c.Hide)
	dismiss.Importance = widget.LowImportance

	c.header = container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(c.copy, dismiss), c.question),
		widget.NewSeparator(),
	)

	c.answer = widget.NewRichText()
	c.answer.Wrapping = fyne.TextWrapWord
	c.scroll = container.NewVScroll(c.answer)

	c.window.SetContent(container.NewPadded(container.NewBorder(c.header, nil, nil, nil, c.scroll)))
	c.window.SetCloseIntercept(c.Hide)
	c.window.Canvas().SetOnTypedKey(func(event *fyne.KeyEvent) {
		if event.Name == fyne.KeyEscape {
			c.Hide()
		}
	})
	c.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { c.copyAnswer() })
}

// fit grows the card with the answer, and past the cap the answer scrolls.
func (c *AnswerCard) fit() fyne.Size {
	padding := theme.Padding()
	c.answer.Resize(fyne.NewSize(answerWidth-2*padding, 0))

	height := c.header.MinSize().Height + c.answer.MinSize().Height + 3*padding
	return fyne.NewSize(answerWidth, min(max(height, answerMinHeight), answerMaxHeight))
}

func (c *AnswerCard) copyAnswer() {
	c.app.Clipboard().SetContent(c.text)
	c.copy.SetIcon(theme.ConfirmIcon())

	time.AfterFunc(copiedFor, func() {
		fyne.Do(func() { c.copy.SetIcon(theme.ContentCopyIcon()) })
	})
}

func (c *AnswerCard) float() {
	native, ok := c.window.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		switch window := context.(type) {
		case driver.X11WindowContext:
			overlay.Panel(window.WindowHandle)
		case driver.WindowsWindowContext:
			overlay.Panel(window.HWND)
		case driver.MacWindowContext:
			overlay.Panel(window.NSWindow)
		}
	})
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
