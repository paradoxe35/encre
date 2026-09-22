package main

import (
	"context"
	"log/slog"
	"time"

	"fyne.io/fyne/v2"
	"github.com/paradoxe35/encre/internal/config"
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
// status bar and the tray every time, and a notification once per release, which
// is remembered so a skipped version does not nag at every launch. Failures stay
// in the log.
func (a *Application) checkForUpdates(stop <-chan struct{}) {
	if a.updater == nil {
		return
	}

	go func() {
		wait := time.NewTimer(updateCheckDelay)
		defer wait.Stop()
		for {
			select {
			case <-stop:
				return
			case <-wait.C:
			}

			if rel := a.latestRelease(); rel != nil {
				fresh := a.noteAnnounced(rel.Tag)
				fyne.Do(func() {
					a.mainWindow.SetAvailableUpdate(rel)
					if fresh {
						a.notifications.ShowInfo("Update available", "Encre "+rel.Tag+" is available")
					}
				})
			}
			wait.Reset(updateCheckInterval)
		}
	}()
}

// noteAnnounced records the release and reports whether it is new to the user.
func (a *Application) noteAnnounced(tag string) bool {
	cfg := a.currentConfig()
	if !firstAnnouncement(cfg, tag) {
		return false
	}
	if err := cfg.Save(); err != nil {
		logger.Error("Failed to remember the announced update", "error", err)
	}
	return true
}

func firstAnnouncement(cfg *config.Config, tag string) bool {
	if cfg.AnnouncedUpdate() == tag {
		return false
	}
	cfg.SetAnnouncedUpdate(tag)
	return true
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
