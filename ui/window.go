package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/history"
	"github.com/paradoxe35/encre/internal/input"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/permissions"
	"github.com/paradoxe35/encre/internal/platform"
	"github.com/paradoxe35/encre/internal/stt"
)

const (
	PaddingSmall  = 5
	PaddingMedium = 10
	PaddingLarge  = 20
)

type MainWindow struct {
	fyne.Window
	app                 fyne.App
	config              *config.Config
	hotkeyManager       *input.FFIHotkeyManager
	permissionPrompt    *permissionPrompt
	rootContainer       *fyne.Container
	mainContent         fyne.CanvasObject
	permissionContainer fyne.CanvasObject

	providerBinding       binding.String
	apiKeyBinding         binding.String
	modelBinding          binding.String
	baseURLBinding        binding.String
	statusBinding         binding.String
	startMinimizedBinding binding.Bool
	startOnLoginBinding   binding.Bool
	startMinimizedCheck   *widget.Check
	startOnLoginCheck     *widget.Check
	themeBinding          binding.String
	unsavedLabel          *widget.Label
	dirty                 bool
	initializing          bool

	hotkeyBindings   map[config.ActionKind]binding.String
	operationEditors map[config.Operation]*operationEditor
	captures         map[config.ActionKind]*HotkeyCapture
	enables          map[config.ActionKind]*widget.Check

	primaryLanguage   *LanguagePicker
	secondaryLanguage *LanguagePicker
	mentionsCheck     *widget.Check

	speechModels        *ModelList
	speechStoreRef      *stt.Store
	historyStore        *history.Store
	refreshHistory      func()
	speechEngine        *widget.Select
	speechLanguage      *widget.Select
	speechLanguageEntry *widget.SelectEntry
	speechLanguageBox   *fyne.Container
	speechLanguageCodes map[string]string
	languageSetKey      string
	speechKeepLoaded    *widget.Check
	speechCleanUp       *widget.Check
	microphone          *MicrophonePicker
	speechRemote        *widget.Select
	speechRemoteModel   *widget.SelectEntry
	speechRemoteURL     *widget.Entry
	speechRemoteKey     *widget.Entry

	baseURLContainer *fyne.Container
	baseURLEntry     *widget.Entry

	providerSelect       *widget.Select
	deleteProviderButton *widget.Button

	onShowCallback func()
	onHideCallback func()
}

func NewMainWindow(app fyne.App, cfg *config.Config, hotkeyManager *input.FFIHotkeyManager) *MainWindow {
	window := newChromelessWindow(app, "Encre")
	window.Resize(fyne.NewSize(windowWidth, windowHeight))
	window.SetFixedSize(true)
	window.CenterOnScreen()
	window.SetIcon(app.Icon())

	prompt := newPermissionPrompt()

	mw := &MainWindow{
		Window:           window,
		app:              app,
		config:           cfg,
		hotkeyManager:    hotkeyManager,
		permissionPrompt: prompt,
	}
	mw.initializing = true

	prompt.restartButton.OnTapped = mw.restartApplication

	mw.initBindings()

	themeName := cfg.Appearance.Theme
	if themeName == "" {
		themeName = "auto"
	}
	mw.applyTheme(themeName)

	mw.mainContent = mw.createContent()
	mw.permissionContainer = mw.permissionPrompt.canvasObject()
	mw.rootContainer = container.NewStack(mw.mainContent, mw.permissionContainer)
	window.SetContent(mw.rootContainer)
	mw.showMainContent()
	mw.initializing = false

	return mw
}

func (w *MainWindow) initBindings() {
	w.providerBinding = binding.NewString()
	w.apiKeyBinding = binding.NewString()
	w.modelBinding = binding.NewString()
	w.baseURLBinding = binding.NewString()
	w.statusBinding = binding.NewString()
	w.startMinimizedBinding = binding.NewBool()
	w.startOnLoginBinding = binding.NewBool()
	w.themeBinding = binding.NewString()

	currentProvider := w.config.GetCurrentProvider()
	w.providerBinding.Set(currentProvider)

	w.loadProviderSettings(currentProvider)

	w.hotkeyBindings = make(map[config.ActionKind]binding.String, len(config.ActionOrder))
	for _, kind := range config.ActionOrder {
		value := binding.NewString()
		value.Set(w.config.Action(kind).Hotkey)
		w.hotkeyBindings[kind] = value
	}

	w.statusBinding.Set("Ready")
	w.startMinimizedBinding.Set(w.config.Appearance.StartMinimized)

	// Re-check actual system state: the user may have removed the login item outside the app.
	autoStart := platform.GetAutoStart()
	actualStartOnLogin := autoStart.IsEnabled()
	w.startOnLoginBinding.Set(actualStartOnLogin)

	// One listener per binding covers both directions without Bind overwriting it.
	w.startMinimizedBinding.AddListener(binding.NewDataListener(w.markDirty))
	w.startOnLoginBinding.AddListener(binding.NewDataListener(w.markDirty))

	if w.config.Appearance.StartOnLogin != actualStartOnLogin {
		w.config.Appearance.StartOnLogin = actualStartOnLogin
		w.config.Save()
		logger.Info("Synced StartOnLogin with system state", "enabled", actualStartOnLogin)
	}

	theme := w.config.Appearance.Theme
	if theme == "" {
		theme = "auto"
	}
	w.themeBinding.Set(theme)
}

// SetPermissionState updates the permission prompt visibility and messaging.
func (w *MainWindow) SetPermissionState(state permissions.State, showRestart bool) {
	if w.permissionPrompt == nil {
		return
	}

	w.permissionPrompt.update(state, showRestart)

	if !state.AllGranted() || showRestart {
		w.showPermissionContent()
	} else {
		w.showMainContent()
	}
}

func (w *MainWindow) showPermissionContent() {
	if w.permissionContainer != nil {
		w.permissionContainer.Show()
	}
	if w.mainContent != nil {
		w.mainContent.Hide()
	}
	if w.rootContainer != nil {
		w.rootContainer.Objects = []fyne.CanvasObject{w.mainContent, w.permissionContainer}
		w.rootContainer.Refresh()
	}
}

func (w *MainWindow) showMainContent() {
	if w.mainContent != nil {
		w.mainContent.Show()
	}
	if w.permissionContainer != nil {
		w.permissionContainer.Hide()
	}
	if w.rootContainer != nil {
		w.rootContainer.Objects = []fyne.CanvasObject{w.permissionContainer, w.mainContent}
		w.rootContainer.Refresh()
	}
}

func (w *MainWindow) createContent() fyne.CanvasObject {
	statusBar := w.createStatusBar()

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("AI", theme.ComputerIcon(), w.createProviderSection()),
		container.NewTabItemWithIcon("Hotkeys", keyboardIcon, w.createHotkeysSection()),
		container.NewTabItemWithIcon("Actions", theme.DocumentIcon(), w.createActionsSection()),
		container.NewTabItemWithIcon("Speech", theme.MediaRecordIcon(), w.createSpeechSection()),
		container.NewTabItemWithIcon("History", theme.HistoryIcon(), w.createHistorySection()),
		container.NewTabItemWithIcon("System", theme.SettingsIcon(), w.createSystemSection()),
	)

	tabs.OnSelected = func(tab *container.TabItem) {
		// History shows other tabs' activity, so it re-reads on every visit.
		if tab.Text == "History" && w.refreshHistory != nil {
			fyne.Do(w.refreshHistory)
		}
		// Works around an AppTabs layout width bug on Windows: https://github.com/fyne-io/fyne/issues/5338
		go func() {
			time.Sleep(50 * time.Millisecond)
			fyne.Do(func() {
				tab.Content.Refresh()
			})
		}()
	}

	saveBtn := widget.NewButtonWithIcon("Save Settings", theme.DocumentSaveIcon(), w.saveSettings)
	saveBtn.Importance = widget.HighImportance
	w.unsavedLabel = widget.NewLabel("Unsaved changes")
	w.unsavedLabel.TextStyle.Bold = true
	w.unsavedLabel.Importance = widget.WarningImportance
	w.unsavedLabel.Hide()

	content := container.NewBorder(
		nil, // top
		container.NewBorder(nil, nil, nil, container.NewHBox(w.unsavedLabel, saveBtn), statusBar), // bottom
		nil,  // left
		nil,  // right
		tabs, // center
	)

	return container.NewPadded(content)
}

func (w *MainWindow) markDirty() {
	if w.dirty {
		return
	}
	if w.initializing {
		return
	}
	w.dirty = true
	if w.unsavedLabel != nil {
		w.unsavedLabel.Show()
		w.unsavedLabel.Refresh()
	}
}

func (w *MainWindow) markClean() {
	w.dirty = false
	if w.unsavedLabel != nil {
		w.unsavedLabel.Hide()
		w.unsavedLabel.Refresh()
	}
}

func (w *MainWindow) historyStoreRef() *history.Store {
	if w.historyStore == nil {
		w.historyStore = history.NewStore()
	}
	return w.historyStore
}

// SetHistoryStore shares the processor's history store with the UI, refreshing an open History tab on new entries.
func (w *MainWindow) SetHistoryStore(store *history.Store) {
	w.historyStore = store
	store.OnChange(func() {
		fyne.Do(func() {
			if w.refreshHistory != nil {
				w.refreshHistory()
			}
		})
	})
}

func (w *MainWindow) ShowWindow() {
	w.Show()
	// Must run after Show: the Dock entry activates the app, which otherwise leaves it frontmost and empty.
	if w.onShowCallback != nil {
		w.onShowCallback()
	}
	w.RequestFocus()

	// Works around a Windows minimize/restore sizing bug: https://github.com/fyne-io/fyne/issues/300
	w.Resize(w.Canvas().Size())
	w.Content().Refresh()
}

// HideWindow hides the window and handles platform-specific behavior (e.g., macOS Dock)
func (w *MainWindow) HideWindow() {
	w.Hide()
	if w.onHideCallback != nil {
		w.onHideCallback()
	}
}

func (w *MainWindow) SetShowHideCallbacks(onShow func(), onHide func()) {
	w.onShowCallback = onShow
	w.onHideCallback = onHide
}
