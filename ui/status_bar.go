package ui

import (
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The window has a fixed size, so past this length the bar can only show an ellipsis.
const statusDetailThreshold = 70

func (w *MainWindow) createStatusBar() fyne.CanvasObject {
	label := widget.NewLabel("")
	label.Bind(w.statusBinding)
	label.Truncation = fyne.TextTruncateEllipsis

	icon := newStatusIcon(w.showStatusDetail)
	w.statusBinding.AddListener(binding.NewDataListener(func() {
		text, _ := w.statusBinding.Get()
		icon.SetActive(utf8.RuneCountInString(text) > statusDetailThreshold)
	}))

	return container.NewBorder(nil, nil, icon, nil, label)
}

func (w *MainWindow) showStatusDetail() {
	text, _ := w.statusBinding.Get()

	message := widget.NewLabel(text)
	message.Wrapping = fyne.TextWrapWord

	copyButton := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		w.app.Clipboard().SetContent(text)
	})

	content := container.NewBorder(nil,
		container.NewHBox(layout.NewSpacer(), copyButton),
		nil, nil,
		container.NewScroll(message),
	)

	detail := dialog.NewCustom("Status", "Close", content, w.Window)
	detail.Resize(fyne.NewSize(460, 280))
	detail.Show()
}

// statusIcon is an info icon that becomes clickable, with a pointer cursor,
// when there is more to read than the bar can show.
type statusIcon struct {
	widget.BaseWidget
	icon     *widget.Icon
	onTapped func()
	active   bool
}

func newStatusIcon(onTapped func()) *statusIcon {
	s := &statusIcon{icon: widget.NewIcon(theme.InfoIcon()), onTapped: onTapped}
	s.ExtendBaseWidget(s)
	return s
}

func (s *statusIcon) SetActive(active bool) {
	s.active = active
}

func (s *statusIcon) Active() bool {
	return s.active
}

func (s *statusIcon) Tapped(*fyne.PointEvent) {
	if s.active {
		s.onTapped()
	}
}

func (s *statusIcon) Cursor() desktop.Cursor {
	if s.active {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

func (s *statusIcon) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.icon)
}
