package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/systray"
	"github.com/paradoxe35/encre/internal/actions"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/input"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/overlay"
	"github.com/paradoxe35/encre/internal/permissions"
	"github.com/paradoxe35/encre/internal/platform"
	"github.com/paradoxe35/encre/internal/stt"
	"github.com/paradoxe35/encre/ui"
)

type Application struct {
	app        fyne.App
	mainWindow *ui.MainWindow

	// configMu guards config: the listener swaps it while hotkey and voice
	// goroutines are reading it.
	configMu sync.RWMutex
	config   *config.Config

	hotkeyManager *input.FFIHotkeyManager
	processor     *actions.Processor
	voice         *actions.Voice
	answers       *ui.AnswerCard
	notifications *ui.NotificationManager
	// updater is nil in development builds.
	updater      ui.Updater
	updateChecks chan struct{}

	permissionMonitorCancel    context.CancelFunc
	permissionsMissingOnLaunch bool
	// overlayMu guards the indicator swap: config listeners run concurrently.
	overlayMu  sync.Mutex
	indicators *indicatorChoice
	indicator  *overlay.Indicator

	reloadMutex sync.Mutex
	// escapeBound is whether Esc closes the answer card, which outlives a hotkey reload.
	escapeBound atomic.Bool
}

const closeAnswerAction = "close_answer"

func NewApplication(app fyne.App, cfg *config.Config) (*Application, error) {
	processor, err := actions.NewProcessor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create processor: %w", err)
	}

	hotkeyManager := input.NewFFIHotkeyManager()
	if hotkeyManager == nil {
		return nil, fmt.Errorf("failed to create FFI hotkey manager")
	}

	notifications := ui.NewNotificationManager(app)
	platform.RegisterNotifier(config.APP_ID, "Encre")

	application := &Application{
		app:           app,
		config:        cfg,
		hotkeyManager: hotkeyManager,
		processor:     processor,
		notifications: notifications,
	}

	// Before the window, which builds its Updates controls from it.
	application.updater, err = newUpdater(app, application.teardown)
	if err != nil {
		return nil, fmt.Errorf("failed to create updater: %w", err)
	}

	mainWindow := ui.NewMainWindow(app, cfg, hotkeyManager, application.updater)
	mainWindow.SetIcon(resourceIconPng)
	mainWindow.SetHistoryStore(processor.History())
	application.mainWindow = mainWindow

	application.answers = ui.NewAnswerCard(app)
	application.answers.SetShowHideCallbacks(func() {
		rememberFrontmostApp()
		application.bindEscape()
	}, func() {
		application.unbindEscape()
		restoreFrontmostApp()
	})
	application.answers.SetTextSize(cfg.AnswerCardSettings().TextSize)
	application.answers.SetStyle(cfg.AnswerCardSettings().Style)
	application.answers.SetOnAsk(func(question string) {
		go processor.AnswerTyped(application.answers, question)
	})

	application.voice = actions.NewVoice(processor,
		application.currentConfig,
		func(kind config.ActionKind, err error) {
			fyne.Do(func() {
				application.notifications.ShowError(kind.Label()+" failed", err.Error())
				// A refused microphone is the one permission that can go missing after launch.
				application.mainWindow.SetPermissionState(permissions.CurrentState(), application.permissionsMissingOnLaunch)
			})
		},
		application.answers)

	application.applyOverlay(cfg)
	input.OnLevel(application.voice.Level)

	// Before hotkeys, so the UI reflects permission state early.
	application.setupPermissions()

	application.setupHotkeys()

	stt.StartRefreshing()
	application.voice.Prepare()

	config.RegisterListener(func(newCfg *config.Config) {
		logger.Info("Config changed, reloading hotkeys")
		application.setConfig(newCfg)
		application.reloadHotkeysFromConfig()
		application.applyOverlay(newCfg)
		application.answers.SetTextSize(newCfg.AnswerCardSettings().TextSize)
		application.answers.SetStyle(newCfg.AnswerCardSettings().Style)
	})

	mainWindow.SetShowHideCallbacks(func() {
		showInDock()
		applyNativeWindowIcons()
	}, hideFromDock)

	if desk, ok := app.(desktop.App); ok {
		desk.SetSystemTrayIcon(trayIcon())
		ui.SetupSystemTray(desk, mainWindow, func() error {
			application.Stop()
			return nil
		})
	}

	app.Lifecycle().SetOnStarted(func() {
		systray.SetTooltip("Encre - revise, translate and dictate text, and ask questions")
		installReopenHandler(application.ShowWindow)
		prepareNotifications()
	})

	mainWindow.SetCloseIntercept(func() {
		mainWindow.HideWindow() // HideWindow, not a raw close, so the hide callbacks still fire
	})

	application.updateChecks = make(chan struct{})
	application.checkForUpdates(application.updateChecks)

	return application, nil
}

func (a *Application) setupHotkeys() {
	for _, kind := range config.ActionOrder {
		action := a.currentConfig().Action(kind)
		if !action.Enabled || action.Hotkey == "" {
			continue
		}

		switch {
		case kind.Listens():
			a.registerVoice(kind, action)
		case kind == config.ActionAskByTyping:
			err := a.hotkeyManager.RegisterHotkey(action.Hotkey, string(kind), a.answers.Prompt)
			a.reportBindingFailure(action.Hotkey, err)
		default:
			err := a.hotkeyManager.RegisterHotkey(action.Hotkey, string(kind), a.actionHandler(kind))
			a.reportBindingFailure(action.Hotkey, err)
		}
	}
}

func (a *Application) registerVoice(kind config.ActionKind, action config.ActionConfig) {
	hold := a.voice.Dictate
	if kind == config.ActionAskByVoice {
		hold = a.voice.Ask
	}

	var err error
	if action.PushToTalk {
		err = a.hotkeyManager.RegisterHoldHotkey(action.Hotkey, string(kind), hold)
	} else {
		err = a.hotkeyManager.RegisterHotkey(action.Hotkey, string(kind), func() {
			hold(!a.voice.Recording(kind))
		})
	}
	a.reportBindingFailure(action.Hotkey, err)
}

func (a *Application) actionHandler(kind config.ActionKind) func() {
	return func() {
		logger.Info("Hotkey triggered", "action", kind)

		if a.processor.IsProcessing() {
			fyne.Do(func() {
				a.notifications.ShowInfo("Please wait", "Another action is already running")
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

// While the answer card shows, Esc closes it whichever window has the keyboard.
func (a *Application) bindEscape() {
	a.escapeBound.Store(true)
	a.registerEscape()
}

func (a *Application) registerEscape() {
	err := a.hotkeyManager.RegisterHotkey("escape", closeAnswerAction, func() { fyne.Do(a.answers.Hide) })
	if err != nil {
		logger.Warn("Esc will only close the answer card while it has the keyboard", "error", err)
	}
}

func (a *Application) unbindEscape() {
	a.escapeBound.Store(false)
	if err := a.hotkeyManager.UnregisterHotkey(closeAnswerAction); err != nil {
		logger.Warn("Could not release Esc", "error", err)
	}
}

// A silent failure would look like a binding the system never delivers.
func (a *Application) reportBindingFailure(binding string, err error) {
	if err == nil {
		return
	}
	logger.Error("Could not register shortcut", "binding", binding, "error", err)
	fyne.Do(func() {
		a.notifications.ShowError("Hotkey not registered", binding+": "+err.Error())
	})
}

// Serialised: listeners run on their own goroutine, and interleaving one reload's
// clear with another's re-registration would leave shortcuts unbound.
func (a *Application) reloadHotkeysFromConfig() {
	a.reloadMutex.Lock()
	defer a.reloadMutex.Unlock()

	if a.hotkeyManager == nil {
		logger.Error("Hotkey manager not initialized")
		return
	}

	// The listener keeps running, so clearing does not spawn a new thread.
	if err := a.hotkeyManager.ClearBindings(); err != nil {
		logger.Error("Failed to clear bindings", "error", err)
		fyne.Do(func() {
			a.notifications.ShowError("Hotkey reload failed", "Could not clear the old hotkeys")
		})
		return
	}

	a.setupHotkeys()
	if a.escapeBound.Load() {
		a.registerEscape()
	}
	logger.Info("Hotkeys reloaded successfully")
}

// indicatorChoice is which features show the indicator; one window serves both.
type indicatorChoice struct {
	voice bool
	text  bool
}

// applyOverlay swaps the indicators only when a choice changed, so a save of
// unrelated settings never interrupts one that is showing.
func (a *Application) applyOverlay(cfg *config.Config) {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()

	indicators := cfg.IndicatorSettings()
	choice := indicatorChoice{
		voice: indicators.Voice,
		text:  indicators.Text,
	}
	if a.indicators != nil && *a.indicators == choice {
		return
	}
	a.indicators = &choice

	if a.indicator != nil {
		a.indicator.Close()
		a.indicator = nil
	}
	if choice.voice || choice.text {
		a.indicator = overlay.New(fyne.DoAndWait)
	}
	a.voice.SetOverlay(a.owner(choice.voice))
	a.processor.SetOverlay(a.owner(choice.text))
}

func (a *Application) owner(on bool) overlay.Overlay {
	if on && a.indicator != nil {
		return a.indicator.Owner()
	}
	return overlay.Disabled{}
}

// closeOverlay takes the window down while the window system is still there.
func (a *Application) closeOverlay() {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	if a.indicator != nil {
		a.indicator.Close()
		a.indicator = nil
	}
}

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
	missingOnLaunch := supported && state.NeedsRestart()

	a.permissionsMissingOnLaunch = missingOnLaunch
	a.mainWindow.SetPermissionState(state, missingOnLaunch)

	if !supported {
		return
	}

	if !state.AllGranted() {
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

			if state.MicrophoneDenied != previous.MicrophoneDenied {
				if state.MicrophoneDenied {
					logger.Warn("Microphone permission refused")
				} else {
					logger.Info("Microphone permission granted")
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

// Safe to call from any goroutine; the instance handover relies on that.
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
				a.notifications.ShowError("Hotkeys are not listening", reason)
			})
		}
	}()

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

	a.teardown()

	// Stop is also called from the tray's Quit handler, off Fyne's thread.
	fyne.Do(a.app.Quit)
}

// teardown releases the hotkeys and background work; an updated copy relaunched
// in our place needs them free before it starts.
func (a *Application) teardown() {
	if a.updateChecks != nil {
		close(a.updateChecks)
		a.updateChecks = nil
	}
	if a.permissionMonitorCancel != nil {
		a.permissionMonitorCancel()
	}
	stt.StopRefreshing()
	a.closeOverlay()

	a.hotkeyManager.Stop()
	a.hotkeyManager.Close()
	a.voice.Close()
	a.processor.Close()
}
