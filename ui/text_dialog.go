package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func showTextDialog(window fyne.Window, title, text string, size fyne.Size) {
	body := widget.NewLabel(text)
	body.Wrapping = fyne.TextWrapWord
	body.Selectable = true

	copyAll := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		fyne.CurrentApp().Clipboard().SetContent(text)
	})
	copyAll.Importance = widget.LowImportance

	content := container.NewBorder(nil,
		container.NewHBox(layout.NewSpacer(), copyAll),
		nil, nil,
		container.NewVScroll(body),
	)

	detail := dialog.NewCustom(title, "Close", content, window)
	detail.Resize(size)
	detail.Show()
}
