// Encre revises, translates and dictates text, and answers questions, with AI, anywhere on your computer.
// Author: Paradoxe Ng <contact@pngwasi.me>
// Repository: https://github.com/paradoxe35/encre

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/ducking"
	"github.com/paradoxe35/encre/internal/input"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/platform"
	"github.com/paradoxe35/encre/internal/utils"
	"github.com/paradoxe35/encre/internal/version"
)

func main() {
	if err := logger.Init(); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	input.ForwardNativeLogs()

	utils.EnsureAppHomeDir()

	lockPath := utils.AppHomeDir("encre.lock")
	portPath := utils.AppHomeDir("instance.port")
	instance, err := platform.Claim(lockPath, portPath)
	if err != nil {
		if platform.Notify(portPath) {
			logger.Info("Handed this launch to the Encre already running")
			return
		}
		logger.Error("Another instance is already running", "error", err)
		os.Exit(1)
	}
	defer instance.Close()

	if err := ducking.RecoverStale(); err != nil {
		logger.Warn("Failed to restore audio an earlier run left lowered", "error", err)
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		cfg = config.Default()
	}

	myApp := app.NewWithID(config.APP_ID)
	myApp.SetIcon(resourceIconPng)

	logger.Info("Encre starting",
		"version", version.GetVersion(myApp),
		"build", version.GetBuildNumber(myApp))

	application, err := NewApplication(myApp, cfg, instance)
	if err != nil {
		logger.Error("Failed to create application", "error", err)
		os.Exit(1)
	}

	instance.Serve(application.ShowWindow)
	stopOnSignal(application)

	if err := application.Start(); err != nil {
		logger.Error("Failed to start application", "error", err)
		os.Exit(1)
	}
}

// A logout or kill must still give back the hotkeys and the audio lowered while dictating.
func stopOnSignal(application *Application) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		received := <-signals
		logger.Info("Stopping on signal", "signal", received.String())
		// A second signal kills outright if stopping hangs.
		signal.Reset(os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		onSignal(received, application.loopStarted.Load(), application.Stop, func() { fyne.Do(application.app.Quit) })
	}()
}

// Fyne quits on SIGINT and SIGTERM itself once its loop runs, and Start cleans up once the loop ends.
// Before that the quit is queued, so the loop ends as it starts and Start's teardown does not race Start.
func onSignal(received os.Signal, loopStarted bool, stop, quit func()) {
	switch {
	case received == syscall.SIGHUP:
		stop()
	case !loopStarted:
		quit()
	}
}
