// Encre - AI-powered text revision tool
// Author: Paradoxe Ng <contact@pngwasi.me>
// Repository: https://github.com/paradoxe35/encre

package main

import (
	"log"
	"os"

	"fyne.io/fyne/v2/app"
	singleinstance "github.com/allan-simon/go-singleinstance"
	"github.com/paradoxe35/encre/internal/config"
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
	lockFile, err := singleinstance.CreateLockFile(lockPath)
	if err != nil {
		if platform.Notify(portPath) {
			logger.Info("Handed this launch to the Encre already running")
			return
		}
		logger.Error("Another instance is already running", "error", err)
		os.Exit(1)
	}
	defer lockFile.Close()

	handover := platform.NewHandover(portPath)
	defer handover.Close()

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

	application, err := NewApplication(myApp, cfg)
	if err != nil {
		logger.Error("Failed to create application", "error", err)
		os.Exit(1)
	}

	handover.Serve(application.ShowWindow)

	if err := application.Start(); err != nil {
		logger.Error("Failed to start application", "error", err)
		os.Exit(1)
	}
}
