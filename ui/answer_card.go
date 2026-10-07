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
)

// AnswerCard shows an answer where the indicator was. Unlike the indicator it takes the keyboard,
// so Esc can close it. Every method runs on the UI thread.
type AnswerCard struct {
	app     fyne.App
	window  fyne.Window
	visible bool
	text    string

	content *widget.RichText
	scroll  *container.Scroll
	footer  fyne.CanvasObject
	copy    *widget.Button

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

func (c *AnswerCard) Show(question, answer string) {
	if c.window == nil {
		c.build()
	}
	spot, placed := c.findSpot()

	c.text = answer
	c.content.Segments = append(questionSegments(question), widget.NewRichTextFromMarkdown(answer).Segments...)
	c.content.Refresh()
	c.copy.SetIcon(theme.ContentCopyIcon())

	size := c.fit()
	c.window.Resize(size)
	c.scroll.ScrollToTop()

	if !c.visible && c.onShow != nil {
		c.onShow()
	}
	c.visible = true
	// The first Show creates the native window, so the first call has nothing to reach yet.
	c.float(spot, placed, size)
	c.window.Show()
	c.float(spot, placed, size)
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

func (c *AnswerCard) float(spot overlay.Spot, placed bool, size fyne.Size) {
	if !placed {
		return
	}
	native := c.window.(driver.NativeWindow)

	scale := c.window.Canvas().Scale()
	frame := spot.Frame(int(size.Width*scale), int(size.Height*scale))
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
