package main

import (
	"context"
	"log/slog"
	"time"

	"fyne.io/fyne/v2"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/version"
	"github.com/paradoxe35/encre/ui"
	updater "github.com/paradoxe35/go-updater"
)

const (
	releaseOwner = "paradoxe35"
	releaseRepo  = "encre"

	// Startup is busy registering hotkeys and loading models; the check can wait.
	updateCheckDelay    = 5 * time.Second
	updateCheckTimeout  = 30 * time.Second
	updateCheckInterval = 6 * time.Hour
)

// newUpdater returns nil for development builds, whose version compares to no release.
func newUpdater(app fyne.App, beforeRelaunch func()) (ui.Updater, error) {
	if !version.IsProduction(app) {
		return nil, nil
	}
	return updater.New(updater.Config{
		Owner:   releaseOwner,
		Repo:    releaseRepo,
		Version: version.GetVersion(app),
		// logger.Init made the file logger the slog default.
		Logger:         slog.Default(),
		BeforeRelaunch: beforeRelaunch,
	})
}

// A tray app runs for days, so the check repeats. A found release reaches the
// status bar, the tray and one notification; failures stay in the log.
func (a *Application) checkForUpdates(stop <-chan struct{}) {
	if a.updater == nil {
		return
	}

	go func() {
		announced := ""
		wait := time.NewTimer(updateCheckDelay)
		defer wait.Stop()
		for {
			select {
			case <-stop:
				return
			case <-wait.C:
			}

			if rel := a.latestRelease(); rel != nil && rel.Tag != announced {
				announced = rel.Tag
				fyne.Do(func() {
					a.mainWindow.SetAvailableUpdate(rel)
					a.notifications.ShowInfo("Update available", "Encre "+rel.Tag+" is available")
				})
			}
			wait.Reset(updateCheckInterval)
		}
	}()
}

func (a *Application) latestRelease() *updater.Release {
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()

	rel, found, err := a.updater.Check(ctx)
	if err != nil {
		logger.Warn("Update check failed", "error", err)
		return nil
	}
	if !found {
		return nil
	}
	return rel
}
