package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// The buttons that started a task stay locked until it settles, so a slow request cannot be fired twice.
type progress interface {
	Busy(message string)
	Done(message string)
	Fail(message string)
}

type feedback struct {
	widget.BaseWidget
	label *widget.Label
	bar   *widget.ProgressBarInfinite
	locks []fyne.Disableable
}

func newFeedback(locks ...fyne.Disableable) *feedback {
	f := &feedback{
		label: widget.NewLabel(""),
		bar:   widget.NewProgressBarInfinite(),
		locks: locks,
	}
	f.label.Wrapping = fyne.TextWrapWord
	f.bar.Hide()
	f.ExtendBaseWidget(f)
	f.Hide()
	return f
}

func (f *feedback) Busy(message string) {
	f.show(message, widget.MediumImportance)
	f.bar.Show()
	f.bar.Start()
	for _, lock := range f.locks {
		lock.Disable()
	}
}

func (f *feedback) Done(message string) {
	f.settle(message, widget.MediumImportance)
}

func (f *feedback) Fail(message string) {
	f.settle(message, widget.DangerImportance)
}

func (f *feedback) Clear() {
	f.settle("", widget.MediumImportance)
}

func (f *feedback) settle(message string, tone widget.Importance) {
	f.bar.Stop()
	f.bar.Hide()
	for _, lock := range f.locks {
		lock.Enable()
	}
	f.show(message, tone)
}

// An empty line takes no space: the dialog only grows when there is something to say.
func (f *feedback) show(message string, tone widget.Importance) {
	f.label.Importance = tone
	f.label.SetText(message)
	if message == "" {
		f.Hide()
	} else {
		f.Show()
	}
}

func (f *feedback) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewVBox(f.bar, f.label))
}

type statusProgress struct {
	window *MainWindow
	locks  []fyne.Disableable
}

func (w *MainWindow) statusProgress(locks ...fyne.Disableable) *statusProgress {
	return &statusProgress{window: w, locks: locks}
}

func (s *statusProgress) Busy(message string) {
	s.window.statusBinding.Set(message)
	for _, lock := range s.locks {
		lock.Disable()
	}
}

func (s *statusProgress) Done(message string) { s.settle(message) }

func (s *statusProgress) Fail(message string) { s.settle(message) }

func (s *statusProgress) settle(message string) {
	for _, lock := range s.locks {
		lock.Enable()
	}
	s.window.statusBinding.Set(message)
}
