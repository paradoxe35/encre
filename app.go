package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/systray"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/input"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/permissions"
	"github.com/paradoxe35/encre/internal/revision"
	"github.com/paradoxe35/encre/internal/stt"
	"github.com/paradoxe35/encre/ui"
)

type Application struct {
	app        fyne.App
	mainWindow *ui.MainWindow

	// configMu guards config: the listener swaps it while hotkey and dictation
	// goroutines are reading it.
	configMu sync.RWMutex
	config   *config.Config

	hotkeyManager *input.FFIHotkeyManager
	processor     *revision.Processor
	dictation     *revision.Dictation
	notifications *ui.NotificationManager

	permissionMonitorCancel    context.CancelFunc
	permissionsMissingOnLaunch bool

	reloadMutex    sync.Mutex
	lastReloadTime time.Time
	reloadDebounce time.Duration
}

func NewApplication(app fyne.App, cfg *config.Config) (*Application, error) {
	processor, err := revision.NewProcessor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create processor: %w", err)
	}

	hotkeyManager := input.NewFFIHotkeyManager()
	if hotkeyManager == nil {
		return nil, fmt.Errorf("failed to create FFI hotkey manager")
	}

	notifications := ui.NewNotificationManager(app)

	mainWindow := ui.NewMainWindow(app, cfg, hotkeyManager)
	mainWindow.SetIcon(resourceIconPng)
	mainWindow.SetHistoryStore(processor.History())

	application := &Application{
		app:            app,
		mainWindow:     mainWindow,
		config:         cfg,
		hotkeyManager:  hotkeyManager,
		processor:      processor,
		notifications:  notifications,
		reloadDebounce: 500 * time.Millisecond,
	}

	application.dictation = revision.NewDictation(processor,
		application.currentConfig,
		func(err error) {
			fyne.Do(func() {
				application.notifications.ShowError("Dictation failed", err.Error())
			})
		})

	// Set up permission monitoring before hotkeys so the UI reflects state early.
	application.setupPermissions()

	application.setupHotkeys()

	stt.RefreshInBackground()
	application.dictation.Prepare()

	config.RegisterListener(func(newCfg *config.Config) {
		logger.Info("Config changed, reloading hotkeys")
		application.setConfig(newCfg)
		application.reloadHotkeysFromConfig()
	})

	mainWindow.SetShowHideCallbacks(showInDock, hideFromDock)

	if desk, ok := app.(desktop.App); ok {
		desk.SetSystemTrayIcon(resourceIconPng)
		ui.SetupSystemTray(desk, mainWindow, func() error {
			application.Stop()
			return nil
		})
	}

	app.Lifecycle().SetOnStarted(func() {
		systray.SetTooltip("Encre - AI Text Revision Tool")
		installReopenHandler(application.ShowWindow)
	})

	mainWindow.SetCloseIntercept(func() {
		mainWindow.HideWindow() // HideWindow, not a raw close, so the hide callbacks still fire
	})

	return application, nil
}

// setupHotkeys binds every enabled action to its shortcut.
func (a *Application) setupHotkeys() {
	for _, kind := range config.ActionOrder {
		action := a.currentConfig().Action(kind)
		if !action.Enabled || action.Hotkey == "" {
			continue
		}

		if kind == config.ActionDictate {
			a.registerDictation(action)
			continue
		}

		err := a.hotkeyManager.RegisterHotkey(action.Hotkey, string(kind), a.actionHandler(kind))
		a.reportBindingFailure(action.Hotkey, err)
	}
}

func (a *Application) registerDictation(action config.ActionConfig) {
	if a.dictation == nil {
		return
	}

	if action.PushToTalk {
		err := a.hotkeyManager.RegisterHoldHotkey(action.Hotkey,
			string(config.ActionDictate), a.dictation.Toggle)
		a.reportBindingFailure(action.Hotkey, err)
		return
	}

	// Toggle mode: each press flips recording, so the same handler serves both.
	recording := false
	err := a.hotkeyManager.RegisterHotkey(action.Hotkey, string(config.ActionDictate), func() {
		recording = !recording
		a.dictation.Toggle(recording)
	})
	a.reportBindingFailure(action.Hotkey, err)
}

func (a *Application) actionHandler(kind config.ActionKind) func() {
	return func() {
		logger.Info("Hotkey triggered", "action", kind)

		if a.processor.IsProcessing() {
			fyne.Do(func() {
				a.notifications.ShowInfo("Please Wait", "Another action is already running")
			})
			return
		}

		if err := a.processor.Run(kind); err != nil {
			logger.Error("Action failed", "action", kind, "error", err)
			fyne.Do(func() {
				a.notifications.ShowError(kind.Label()+" failed", err.Error())
			})
		}
	}
}

// reportBindingFailure surfaces a failed shortcut registration; silent failure would be
// indistinguishable from a binding the system just never delivers.
func (a *Application) reportBindingFailure(binding string, err error) {
	if err == nil {
		return
	}
	logger.Error("Could not register shortcut", "binding", binding, "error", err)
	fyne.Do(func() {
		a.notifications.ShowError("Shortcut not registered", binding+": "+err.Error())
	})
}

// reloadHotkeysFromConfig re-registers all hotkeys against the current config.
func (a *Application) reloadHotkeysFromConfig() {
	a.reloadMutex.Lock()
	now := time.Now()
	if now.Sub(a.lastReloadTime) < a.reloadDebounce {
		logger.Info("Skipping duplicate reload (debounced)")
		a.reloadMutex.Unlock()
		return
	}
	a.lastReloadTime = now
	a.reloadMutex.Unlock()

	logger.Info("Reloading hotkeys from updated config")

	if a.hotkeyManager == nil {
		logger.Error("Hotkey manager not initialized")
		return
	}

	// Bindings are cleared but the listener keeps running, so no new thread is spawned.
	logger.Info("Clearing existing hotkey bindings")
	if err := a.hotkeyManager.ClearBindings(); err != nil {
		logger.Error("Failed to clear bindings", "error", err)
		fyne.Do(func() {
			a.notifications.ShowError("Hotkey Reload Failed", "Failed to clear old hotkeys")
		})
		return
	}

	logger.Info("Re-registering hotkeys with new config")
	a.setupHotkeys()

	// The running listener picks up updated bindings without a stop/start cycle.
	logger.Info("Hotkeys reloaded successfully")
}

// setupPermissions initialises macOS permission handling and keeps the UI in sync.
func (a *Application) currentConfig() *config.Config {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.config
}

func (a *Application) setConfig(cfg *config.Config) {
	a.configMu.Lock()
	a.config = cfg
	a.configMu.Unlock()
}

func (a *Application) setupPermissions() {
	state := permissions.CurrentState()
	supported := permissions.IsSupported()
	missingOnLaunch := supported && !state.AllGranted()

	a.permissionsMissingOnLaunch = missingOnLaunch
	a.mainWindow.SetPermissionState(state, missingOnLaunch)

	if !supported {
		return
	}

	if missingOnLaunch {
		logger.Warn("macOS permissions required for full functionality")
		for _, perm := range state.Missing() {
			logger.Warn("permission pending", "name", perm.DisplayName())
		}

		ctx, cancel := context.WithCancel(context.Background())
		a.permissionMonitorCancel = cancel
		go a.monitorPermissions(ctx, state)
	} else {
		logger.Info("All required macOS permissions granted")
	}
}

func (a *Application) monitorPermissions(ctx context.Context, previous permissions.State) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := permissions.CurrentState()

			if state.AccessibilityGranted != previous.AccessibilityGranted {
				if state.AccessibilityGranted {
					logger.Info("Accessibility permission granted")
				} else {
					logger.Warn("Accessibility permission revoked")
				}
			}

			if state.InputMonitoringGranted != previous.InputMonitoringGranted {
				if state.InputMonitoringGranted {
					logger.Info("Input Monitoring permission granted")
				} else {
					logger.Warn("Input Monitoring permission revoked")
				}
			}

			if state != previous {
				fyne.Do(func() {
					a.mainWindow.SetPermissionState(state, a.permissionsMissingOnLaunch)
				})
				previous = state
			}

			if state.AllGranted() {
				return
			}
		}
	}
}

// ShowWindow brings the settings window up. Safe to call from any goroutine, which is what the
// instance handover needs.
func (a *Application) ShowWindow() {
	fyne.Do(a.mainWindow.ShowWindow)
}

func (a *Application) Start() error {
	if err := a.hotkeyManager.Start(); err != nil {
		return fmt.Errorf("failed to start hotkey manager: %w", err)
	}

	logger.Info("Application started successfully")

	// Start only spawns the listener thread; the OS can still refuse the key tap on it, silently.
	go func() {
		time.Sleep(2 * time.Second)
		if reason := a.hotkeyManager.ListenError(); reason != "" {
			logger.Error("Shortcuts will not fire", "reason", reason)
			fyne.Do(func() {
				a.notifications.ShowError("Shortcuts are not listening", reason)
			})
		}
	}()

	// Show the window if permissions are pending, on first run, or unless starting minimized.
	if a.permissionsMissingOnLaunch {
		a.mainWindow.ShowWindow()
		logger.Info("Showing permissions screen", "permissions_pending", true)
	} else if cfg := a.currentConfig(); cfg.FirstRun() || !cfg.AppearanceSettings().StartMinimized {
		a.mainWindow.ShowWindow()
		if cfg.FirstRun() {
			cfg.SetFirstRun(false)
			if err := cfg.Save(); err != nil {
				logger.Error("Failed to persist first-run flag", "error", err)
			}
			logger.Info("First run complete, window shown")
		}
	} else {
		hideFromDock()
		logger.Info("Starting minimized to tray")
	}

	a.app.Run()
	return nil
}

func (a *Application) Stop() {
	logger.Info("Stopping application")

	if a.permissionMonitorCancel != nil {
		a.permissionMonitorCancel()
	}

	if a.hotkeyManager != nil {
		a.hotkeyManager.Stop()
		a.hotkeyManager.Close()
	}

	if a.dictation != nil {
		a.dictation.Close()
	}

	if a.processor != nil {
		a.processor.Close()
	}

	// Stop is also called from the tray's Quit handler, off Fyne's thread.
	fyne.Do(a.app.Quit)
}
