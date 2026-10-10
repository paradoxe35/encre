package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/permissions"
)

// An unsigned Encre no longer matches an entry made for another copy, so the entry is replaced, not kept.
const pendingPermissionsInfo = "Encre needs these permissions to work. For each one:\n" +
	"1. Click Grant access.\n" +
	"2. If Encre is already in the list, select it and remove it with −.\n" +
	"3. Add Encre with + and switch it on."

type permissionPrompt struct {
	root                   *fyne.Container
	infoLabel              *widget.Label
	accessibilitySection   fyne.CanvasObject
	inputMonitoringSection fyne.CanvasObject
	microphoneSection      fyne.CanvasObject
	dividerAboveList       *widget.Separator
	dividerBelowList       *widget.Separator
	restartRow             fyne.CanvasObject
	restartButton          *widget.Button
}

func newPermissionPrompt() *permissionPrompt {
	title := widget.NewLabelWithStyle("Permissions required", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	info := widget.NewLabel(pendingPermissionsInfo)

	accessibilityButton := newPermissionButton("Grant access", func() {
		permissions.OpenPreference(permissions.Accessibility)
	})

	accessibilitySection := buildPermissionRow(
		"Accessibility",
		"Required to automate keyboard input and clipboard operations.",
		accessibilityButton,
	)

	inputMonitoringButton := newPermissionButton("Grant access", func() {
		permissions.OpenPreference(permissions.InputMonitoring)
	})

	inputMonitoringSection := buildPermissionRow(
		"Input Monitoring",
		"Required to listen for hotkeys in the background.",
		inputMonitoringButton,
	)

	microphoneButton := newPermissionButton("Grant access", func() {
		permissions.OpenPreference(permissions.Microphone)
	})

	microphoneSection := buildPermissionRow(
		"Microphone",
		microphoneRefused,
		microphoneButton,
	)

	restartButton := widget.NewButtonWithIcon("Restart Encre", theme.MediaReplayIcon(), nil)
	restartButton.Importance = widget.HighImportance

	restartRow := container.NewPadded(
		container.NewCenter(restartButton),
	)
	restartRow.Hide()

	dividerAbove := widget.NewSeparator()
	dividerBelow := widget.NewSeparator()
	dividerBelow.Hide()

	body := container.NewVBox(
		title,
		info,
		dividerAbove,
		accessibilitySection,
		inputMonitoringSection,
		microphoneSection,
		dividerBelow,
		restartRow,
	)

	root := container.NewPadded(body)
	root.Hide()

	return &permissionPrompt{
		root:                   root,
		infoLabel:              info,
		accessibilitySection:   accessibilitySection,
		inputMonitoringSection: inputMonitoringSection,
		microphoneSection:      microphoneSection,
		dividerAboveList:       dividerAbove,
		dividerBelowList:       dividerBelow,
		restartRow:             restartRow,
		restartButton:          restartButton,
	}
}

func (p *permissionPrompt) update(state permissions.State, showRestart bool) {
	pending := state.NeedsRestart()
	rows := map[fyne.CanvasObject]bool{
		p.accessibilitySection:   !state.AccessibilityGranted,
		p.inputMonitoringSection: !state.InputMonitoringGranted,
		p.microphoneSection:      pending && state.MicrophoneDenied,
	}
	for row, show := range rows {
		if show {
			row.Show()
		} else {
			row.Hide()
		}
	}

	switch {
	case pending:
		p.infoLabel.SetText(pendingPermissionsInfo)
		p.dividerAboveList.Show()
		p.dividerBelowList.Hide()
		p.restartRow.Hide()
		p.root.Show()
	case showRestart:
		p.infoLabel.SetText("All permissions granted. Restart Encre to use them.")
		p.dividerAboveList.Hide()
		p.dividerBelowList.Show()
		p.restartRow.Show()
		p.root.Show()
	default:
		p.restartRow.Hide()
		p.root.Hide()
	}

	p.root.Refresh()
}

func buildPermissionRow(title, description string, button *widget.Button) fyne.CanvasObject {
	titleLabel := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	descriptionLabel := widget.NewLabel(description)
	descriptionLabel.Wrapping = fyne.TextWrapWord

	textColumn := container.NewVBox(titleLabel, descriptionLabel)

	row := container.NewBorder(nil, nil, nil, button, textColumn)
	return container.NewPadded(row)
}

func newPermissionButton(label string, tapped func()) *widget.Button {
	btn := widget.NewButtonWithIcon(label, theme.SettingsIcon(), tapped)
	btn.Importance = widget.MediumImportance
	btn.IconPlacement = widget.ButtonIconLeadingText
	return btn
}
