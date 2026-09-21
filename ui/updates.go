package ui

import (
	"context"
	"fmt"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/logger"
	updater "github.com/paradoxe35/go-updater"
)

// Updater is the part of go-updater the Updates controls drive; nil means this build cannot update itself.
type Updater interface {
	Check(ctx context.Context) (*updater.Release, bool, error)
	Update(ctx context.Context, rel *updater.Release, opts ...updater.Option) error
}

const updateCheckTimeout = 30 * time.Second

type updatePhase int

const (
	updateIdle updatePhase = iota
	updateChecking
	updateRunning
)

// updateState is the Updates section without its widgets, so the transitions can be tested on their own.
type updateState struct {
	phase   updatePhase
	release *updater.Release
	status  string
	failed  bool
}

func newUpdateState() updateState {
	return updateState{status: "Encre checks for updates automatically."}
}

func (s *updateState) checking() { s.set(updateChecking, "Checking for updates", false) }

func (s *updateState) upToDate() {
	s.release = nil
	s.set(updateIdle, "Encre is up to date", false)
}

func (s *updateState) found(rel *updater.Release) {
	s.release = rel
	s.set(updateIdle, "Version "+rel.Tag+" is available", false)
}

func (s *updateState) checkFailed(err error) {
	s.set(updateIdle, "Could not check for updates: "+err.Error(), true)
}

func (s *updateState) updating(p updater.Progress) { s.set(updateRunning, progressText(p), false) }

// The release stays known so the user can try again.
func (s *updateState) updateFailed(err error) {
	s.set(updateIdle, "Update failed: "+err.Error(), true)
}

func (s *updateState) set(phase updatePhase, status string, failed bool) {
	s.phase = phase
	s.status = status
	s.failed = failed
}

func (s updateState) busy() bool { return s.phase != updateIdle }

func (s updateState) canUpdate() bool { return s.release != nil && !s.busy() }

func progressText(p updater.Progress) string {
	switch p.Stage {
	case updater.Checking:
		return "Checking for updates"
	case updater.Downloading:
		switch {
		case p.Total > 0:
			return fmt.Sprintf("Downloading %s of %s", megabytes(p.Downloaded), megabytes(p.Total))
		case p.Downloaded > 0:
			return fmt.Sprintf("Downloading %s", megabytes(p.Downloaded))
		}
		return "Downloading"
	case updater.Verifying:
		return "Verifying"
	case updater.Installing:
		return "Installing"
	case updater.Relaunching:
		return "Restarting"
	}
	return string(p.Stage)
}

func megabytes(bytes int64) string {
	return fmt.Sprintf("%.0f MB", math.Round(float64(bytes)/1e6))
}

// updatePanel is the Updates block of the System tab.
type updatePanel struct {
	updater      Updater
	state        updateState
	status       *widget.Label
	checkButton  *widget.Button
	updateButton *widget.Button
}

func newUpdatePanel(u Updater) *updatePanel {
	p := &updatePanel{updater: u, state: newUpdateState(), status: widget.NewLabel("")}
	p.status.Wrapping = fyne.TextWrapWord
	p.checkButton = widget.NewButton("Check for updates", p.checkForUpdates)
	p.updateButton = widget.NewButtonWithIcon("Update", theme.DownloadIcon(), p.runUpdate)
	p.render()
	return p
}

func (p *updatePanel) content() fyne.CanvasObject {
	return container.NewVBox(
		container.NewBorder(nil, nil, nil, p.updateButton, p.status),
		container.NewHBox(p.checkButton),
	)
}

// announce shows a release the startup check found; a check or update in flight already knows better.
func (p *updatePanel) announce(rel *updater.Release) {
	if p.state.busy() {
		return
	}
	p.state.found(rel)
	p.render()
}

func (p *updatePanel) render() {
	p.status.Importance = widget.MediumImportance
	if p.state.failed {
		p.status.Importance = widget.DangerImportance
	}
	p.status.SetText(p.state.status)

	setEnabled(p.checkButton, !p.state.busy())
	if p.state.release == nil {
		p.updateButton.Hide()
		return
	}
	p.updateButton.Show()
	setEnabled(p.updateButton, p.state.canUpdate())
}

func (p *updatePanel) checkForUpdates() {
	p.state.checking()
	p.render()
	go p.check()
}

// check runs off Fyne's thread and reports back through fyne.Do.
func (p *updatePanel) check() {
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()

	rel, found, err := p.updater.Check(ctx)
	fyne.Do(func() {
		switch {
		case err != nil:
			logger.Error("Update check failed", "error", err)
			p.state.checkFailed(err)
		case found:
			p.state.found(rel)
		default:
			p.state.upToDate()
		}
		p.render()
	})
}

func (p *updatePanel) runUpdate() {
	if !p.state.canUpdate() {
		return
	}
	rel := p.state.release
	p.state.updating(updater.Progress{Stage: updater.Downloading})
	p.render()
	go p.install(rel)
}

// install runs off Fyne's thread; on success the process is replaced before Update returns.
func (p *updatePanel) install(rel *updater.Release) {
	shown := ""
	report := func(progress updater.Progress) {
		// The download reports every read; only a changed line is worth a trip to the UI thread.
		text := progressText(progress)
		if text == shown {
			return
		}
		shown = text
		fyne.Do(func() {
			p.state.updating(progress)
			p.render()
		})
	}

	err := p.updater.Update(context.Background(), rel, updater.WithProgress(report))
	if err == nil {
		return
	}
	logger.Error("Update failed", "release", rel.Tag, "error", err)
	fyne.Do(func() {
		p.state.updateFailed(err)
		p.render()
	})
}

func setEnabled(button *widget.Button, enabled bool) {
	if enabled {
		button.Enable()
	} else {
		button.Disable()
	}
}
