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
	updater "github.com/paradoxe35/go-updater"
)

const (
	windowWidth  = 600
	windowHeight = 600
)

type MainWindow struct {
	fyne.Window
	app              fyne.App
	config           *config.Config
	hotkeyManager    *input.FFIHotkeyManager
	permissionPrompt *permissionPrompt
	rootContainer    *fyne.Container
	mainContent      fyne.CanvasObject

	providerBinding       binding.String
	apiKeyBinding         binding.String
	modelBinding          binding.String
	baseURLBinding        binding.String
	statusBinding         binding.String
	startMinimizedBinding binding.Bool
	startOnLoginBinding   binding.Bool
	dictationIndicator    binding.Bool
	actionIndicator       binding.Bool
	themeBinding          binding.String
	pasteShortcutBinding  binding.String
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
	speechLanguageDraft string
	speechModelDraft    string
	speechKeepLoaded    *widget.Check
	speechCleanUp       *widget.Check
	microphone          *MicrophonePicker
	speechRemote        *widget.Select
	speechRemoteModel   *widget.SelectEntry
	speechRemoteURL     *widget.Entry
	speechRemoteKey     *widget.Entry
	microphoneNotice    *fyne.Container

	baseURLContainer *fyne.Container

	providerSelect       *widget.Select
	deleteProviderButton *widget.Button

	// updates is nil in builds that cannot update themselves.
	updates *updatePanel
	tabs    *container.AppTabs
	tray    *fyne.Menu
	// trayUpdate is the single "Update to vX" entry; a newer tag relabels it.
	trayUpdate *fyne.MenuItem

	onShowCallback func()
	onHideCallback func()
}

func NewMainWindow(app fyne.App, cfg *config.Config, hotkeyManager *input.FFIHotkeyManager, appUpdater Updater) *MainWindow {
	window := app.NewWindow("Encre")
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
	if appUpdater != nil {
		mw.updates = newUpdatePanel(appUpdater)
		mw.updates.onFound = mw.addTrayUpdateItem
	}
	mw.initializing = true

	prompt.restartButton.OnTapped = mw.restartApplication

	mw.initBindings()

	themeName, _ := mw.themeBinding.Get()
	mw.applyTheme(themeName)

	mw.mainContent = mw.createContent()
	mw.rootContainer = container.NewStack(mw.mainContent, prompt.root)
	window.SetContent(mw.rootContainer)
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
	w.dictationIndicator = binding.NewBool()
	w.actionIndicator = binding.NewBool()
	w.themeBinding = binding.NewString()
	w.pasteShortcutBinding = binding.NewString()
	w.pasteShortcutBinding.Set(string(w.config.PasteShortcut()))

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
	w.dictationIndicator.Set(w.config.Appearance.DictationIndicator)
	w.actionIndicator.Set(w.config.Appearance.ActionIndicator)

	// Re-check actual system state: the user may have removed the login item outside the app.
	autoStart := platform.GetAutoStart()
	actualStartOnLogin := autoStart.IsEnabled()
	w.startOnLoginBinding.Set(actualStartOnLogin)

	// One listener per binding covers both directions without Bind overwriting it.
	w.startMinimizedBinding.AddListener(binding.NewDataListener(w.markDirty))
	w.dictationIndicator.AddListener(binding.NewDataListener(w.markDirty))
	w.actionIndicator.AddListener(binding.NewDataListener(w.markDirty))
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

// A refused microphone only stops dictation, so it is a Speech tab notice
// rather than the blocking permission card.
func (w *MainWindow) SetPermissionState(state permissions.State, showRestart bool) {
	// The card shows and hides itself; only the main content needs to make way for it.
	w.permissionPrompt.update(state, showRestart)
	if state.NeedsRestart() || showRestart {
		w.mainContent.Hide()
	} else {
		w.mainContent.Show()
	}
	w.rootContainer.Refresh()

	if state.MicrophoneDenied {
		w.microphoneNotice.Show()
	} else {
		w.microphoneNotice.Hide()
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

	w.tabs = tabs
	tabs.OnSelected = func(tab *container.TabItem) {
		// History shows other tabs' activity, so it re-reads on every visit.
		if tab.Text == "History" {
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
		nil,
		container.NewBorder(nil, nil, nil, container.NewHBox(w.unsavedLabel, saveBtn), statusBar),
		nil,
		nil,
		tabs,
	)

	return container.NewPadded(content)
}

func (w *MainWindow) markDirty() {
	if w.dirty || w.initializing {
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

func (w *MainWindow) SetHistoryStore(store *history.Store) {
	w.historyStore = store
	store.OnChange(func() { fyne.Do(w.refreshHistory) })
}

// SetAvailableUpdate surfaces a release the startup check found; it must run on Fyne's thread.
func (w *MainWindow) SetAvailableUpdate(rel *updater.Release) {
	w.statusBinding.Set("Encre " + rel.Tag + " is available")
	if w.updates != nil {
		w.updates.announce(rel)
	}
	w.addTrayUpdateItem(rel)
}

// The tray gains an "Update to vX" entry above Settings, the way most tray apps announce one.
func (w *MainWindow) addTrayUpdateItem(rel *updater.Release) {
	if w.tray == nil || w.updates == nil {
		return
	}
	action := func() {
		fyne.Do(func() {
			w.ShowUpdates()
			w.updates.runUpdate()
		})
	}
	if w.trayUpdate != nil {
		w.trayUpdate.Label = trayUpdateLabel(rel)
		w.trayUpdate.Action = action
		w.tray.Refresh()
		return
	}

	w.trayUpdate = fyne.NewMenuItem(trayUpdateLabel(rel), action)
	w.tray.Items = append([]*fyne.MenuItem{w.trayUpdate, fyne.NewMenuItemSeparator()}, w.tray.Items...)
	w.tray.Refresh()
}

func trayUpdateLabel(rel *updater.Release) string {
	return "Update to " + rel.Tag + "…"
}

// ShowUpdates opens the window on the System tab, where update progress is shown.
func (w *MainWindow) ShowUpdates() {
	w.ShowWindow()
	if w.tabs == nil {
		return
	}
	for i, tab := range w.tabs.Items {
		if tab.Text == "System" {
			w.tabs.SelectIndex(i)
			return
		}
	}
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
