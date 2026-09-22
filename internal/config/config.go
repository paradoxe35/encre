package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	locale "github.com/jeandeaual/go-locale"
	"github.com/paradoxe35/encre/internal/language"
	"github.com/paradoxe35/encre/internal/utils"
)

const (
	ProviderTypeOpenAICompatible = "openai-compatible"

	BuiltInOpenAI = "openai"
	BuiltInClaude = "claude"
	BuiltInGemini = "gemini"

	DefaultCharacterLimit = 1000
	DefaultTimeoutSeconds = 30
)

type Config struct {
	mu         sync.RWMutex
	AIProvider AIProviderConfig              `json:"ai_provider"`
	Actions    map[ActionKind]ActionConfig   `json:"actions"`
	Operations map[Operation]OperationConfig `json:"operations"`
	Translate  TranslateConfig               `json:"translate"`
	Speech     SpeechConfig                  `json:"speech"`
	Appearance AppearanceConfig              `json:"appearance"`
	Meta       MetaConfig                    `json:"meta"`

	// EnableProviderMentions lets a selection opt into a provider by starting
	// with "@name". Applies to every AI-backed action.
	EnableProviderMentions bool `json:"enable_provider_mentions"`

	// Paste only matters on Linux: macOS always pastes with Cmd+V.
	Paste PasteShortcut `json:"paste_shortcut"`
}

type ProviderSettings struct {
	APIKey       string  `json:"api_key"`
	BaseURL      string  `json:"base_url,omitempty"`
	Model        string  `json:"model,omitempty"`
	Temperature  float64 `json:"temperature,omitempty"`
	IsCustom     bool    `json:"is_custom,omitempty"`
	ProviderType string  `json:"provider_type,omitempty"`
	// NoAPIKey suits a model running on this machine. Absent means a key is required.
	NoAPIKey bool `json:"no_api_key,omitempty"`
	// LowReasoning asks a reasoning model to think less. Off by default: a model that does not
	// reason rejects the parameter, and the retry that recovers from it costs a round trip.
	LowReasoning bool `json:"low_reasoning,omitempty"`
}

func (s ProviderSettings) RequiresAPIKey() bool {
	return !s.NoAPIKey
}

type AIProviderConfig struct {
	Provider  string                      `json:"provider"` // "openai" | "claude" | "gemini"
	Providers map[string]ProviderSettings `json:"providers"`
}

type TranslateConfig struct {
	PrimaryLanguage   string `json:"primary_language"`
	SecondaryLanguage string `json:"secondary_language"`
}

type AppearanceConfig struct {
	Theme          string `json:"theme"` // "auto" | "light" | "dark"
	StartMinimized bool   `json:"start_minimized"`
	StartOnLogin   bool   `json:"start_on_login"`
	// Indicators is absent from files written before it existed; absent means both on.
	Indicators *IndicatorsConfig `json:"indicators,omitempty"`
}

// IndicatorsConfig is the floating indicator, per feature. It is written whole, so
// a switch turned off stays off.
type IndicatorsConfig struct {
	Dictation bool `json:"dictation"`
	Actions   bool `json:"actions"`
}

func defaultIndicators() *IndicatorsConfig {
	return &IndicatorsConfig{Dictation: true, Actions: true}
}

type MetaConfig struct {
	FirstRun bool `json:"first_run"`
	// AnnouncedUpdate is the last release the user was told about; a restart must not repeat it.
	AnnouncedUpdate string `json:"announced_update,omitempty"`
}

var (
	listeners     []func(*Config)
	listenerMutex sync.RWMutex
)

const APP_ID = "me.pngwasi.encre"

func ConfigPath() string {
	return utils.AppHomeDir("config.json")
}

func Default() *Config {
	return &Config{
		AIProvider: AIProviderConfig{
			Provider:  "openai",
			Providers: defaultProviders(),
		},
		Actions:                DefaultActions(),
		Operations:             DefaultOperations(),
		Translate:              defaultTranslate(),
		Speech:                 defaultSpeech(),
		Appearance:             defaultAppearance(),
		Meta:                   MetaConfig{FirstRun: true},
		EnableProviderMentions: true,
		Paste:                  PasteStandard,
	}
}

func defaultProviders() map[string]ProviderSettings {
	return map[string]ProviderSettings{
		"openai": {
			BaseURL:     "https://api.openai.com/v1",
			Model:       "gpt-4o",
			Temperature: 1.0,
		},
		"claude": {
			BaseURL:     "https://api.anthropic.com",
			Model:       "claude-3-5-haiku-20241022",
			Temperature: 1.0,
		},
		"gemini": {
			BaseURL:     "https://generativelanguage.googleapis.com",
			Model:       "gemini-2.5-flash",
			Temperature: 1.0,
		},
	}
}

func defaultAppearance() AppearanceConfig {
	return AppearanceConfig{
		Theme:          "auto",
		StartMinimized: false,
		StartOnLogin:   false,
		Indicators:     defaultIndicators(),
	}
}

func Load() (*Config, error) {
	configDir := filepath.Dir(ConfigPath())
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	if _, err := os.Stat(ConfigPath()); os.IsNotExist(err) {
		cfg := Default()
		if err := cfg.write(); err != nil {
			return nil, fmt.Errorf("failed to save default config: %w", err)
		}
		return cfg, nil
	}

	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	cfg.applyDefaults()

	if cfg.AIProvider.Providers != nil {
		for name, settings := range cfg.AIProvider.Providers {
			if settings.Temperature == 0 {
				settings.Temperature = 1.0
				cfg.AIProvider.Providers[name] = settings
			}
		}
	}

	return cfg, nil
}

// write persists without notifying, so a default config can be written during Load.
func (c *Config) write() error {
	c.mu.RLock()
	data, err := json.MarshalIndent(c, "", "  ")
	c.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(ConfigPath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

func (c *Config) Save() error {
	if err := c.write(); err != nil {
		return err
	}
	notifyListeners(c)
	return nil
}

func RegisterListener(listener func(*Config)) {
	listenerMutex.Lock()
	defer listenerMutex.Unlock()
	listeners = append(listeners, listener)
}

func notifyListeners(cfg *Config) {
	listenerMutex.RLock()
	defer listenerMutex.RUnlock()

	for _, listener := range listeners {
		go listener(cfg)
	}
}

func (c *Config) GetProviderSettings(provider string) ProviderSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.AIProvider.Providers == nil {
		return ProviderSettings{}
	}

	settings, ok := c.AIProvider.Providers[provider]
	if !ok {
		return defaultProviders()[provider]
	}

	if settings.Temperature == 0 {
		settings.Temperature = 1.0
	}

	return settings
}

func (c *Config) SetProviderSettings(provider string, settings ProviderSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.AIProvider.Providers == nil {
		c.AIProvider.Providers = make(map[string]ProviderSettings)
	}

	if settings.Temperature == 0 {
		settings.Temperature = 1.0
	}

	c.AIProvider.Providers[provider] = settings
}

// The value fields below are read from hotkey and dictation goroutines while the
// UI writes them, so they go through the mutex the map fields already use.

func (c *Config) Translation() TranslateConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Translate
}

func (c *Config) SetTranslation(translate TranslateConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Translate = translate
}

func (c *Config) SpeechSettings() SpeechConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Speech
}

func (c *Config) SetSpeechSettings(speech SpeechConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Speech = speech
}

func (c *Config) AppearanceSettings() AppearanceConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Appearance
}

func (c *Config) SetAppearanceSettings(appearance AppearanceConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Appearance = appearance
}

// IndicatorSettings is never missing: a config without them gets the defaults.
func (c *Config) IndicatorSettings() IndicatorsConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Appearance.Indicators == nil {
		return *defaultIndicators()
	}
	return *c.Appearance.Indicators
}

func (c *Config) FirstRun() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Meta.FirstRun
}

func (c *Config) SetFirstRun(first bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Meta.FirstRun = first
}

func (c *Config) AnnouncedUpdate() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Meta.AnnouncedUpdate
}

func (c *Config) SetAnnouncedUpdate(tag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Meta.AnnouncedUpdate = tag
}

func (c *Config) ProviderMentionsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EnableProviderMentions
}

func (c *Config) SetProviderMentionsEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.EnableProviderMentions = enabled
}

func (c *Config) GetCurrentProvider() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AIProvider.Provider
}

func (c *Config) SetCurrentProvider(provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AIProvider.Provider = provider
}

func BuiltInProviders() []string {
	return []string{BuiltInOpenAI, BuiltInClaude, BuiltInGemini}
}

func IsBuiltInProvider(name string) bool {
	nameLower := strings.ToLower(name)
	for _, p := range BuiltInProviders() {
		if strings.ToLower(p) == nameLower {
			return true
		}
	}
	return false
}

func (c *Config) GetAllProviderNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	names := BuiltInProviders()

	customNames := make([]string, 0)
	for name, settings := range c.AIProvider.Providers {
		if settings.IsCustom {
			customNames = append(customNames, name)
		}
	}
	sort.Strings(customNames)
	names = append(names, customNames...)
	return names
}

func (c *Config) GetConfiguredProviderNames() []string {
	c.mu.RLock()
	providers := make(map[string]ProviderSettings, len(c.AIProvider.Providers))
	for name, settings := range c.AIProvider.Providers {
		providers[name] = settings
	}
	c.mu.RUnlock()

	configured := make([]string, 0, len(providers))
	for name, settings := range providers {
		if strings.TrimSpace(settings.Model) == "" || strings.TrimSpace(settings.BaseURL) == "" {
			continue
		}
		if settings.RequiresAPIKey() {
			apiKey, err := c.GetAPIKey(name)
			if err != nil || strings.TrimSpace(apiKey) == "" {
				continue
			}
		}
		configured = append(configured, name)
	}

	sort.SliceStable(configured, func(i, j int) bool {
		leftBuiltIn := IsBuiltInProvider(configured[i])
		rightBuiltIn := IsBuiltInProvider(configured[j])
		if leftBuiltIn != rightBuiltIn {
			return leftBuiltIn
		}
		return strings.ToLower(configured[i]) < strings.ToLower(configured[j])
	})
	return configured
}

func isValidProviderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func (c *Config) AddCustomProvider(name string, settings ProviderSettings) error {
	if name == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if !isValidProviderName(name) {
		return fmt.Errorf("provider name can only contain letters, numbers, hyphens, and underscores")
	}
	if IsBuiltInProvider(name) {
		return fmt.Errorf("cannot use built-in provider name: %s", name)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.AIProvider.Providers == nil {
		c.AIProvider.Providers = make(map[string]ProviderSettings)
	}

	nameLower := strings.ToLower(name)
	for existingName := range c.AIProvider.Providers {
		if strings.ToLower(existingName) == nameLower {
			return fmt.Errorf("provider with name '%s' already exists", existingName)
		}
	}

	settings.IsCustom = true
	c.AIProvider.Providers[name] = settings
	return nil
}

func (c *Config) DeleteCustomProvider(name string) error {
	if IsBuiltInProvider(name) {
		return fmt.Errorf("cannot delete built-in provider: %s", name)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	settings, exists := c.AIProvider.Providers[name]
	if !exists {
		return fmt.Errorf("provider '%s' not found", name)
	}
	if !settings.IsCustom {
		return fmt.Errorf("cannot delete non-custom provider: %s", name)
	}

	delete(c.AIProvider.Providers, name)

	if c.AIProvider.Provider == name {
		c.AIProvider.Provider = BuiltInOpenAI
	}

	return nil
}

func (c *Config) IsCustomProvider(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if settings, exists := c.AIProvider.Providers[name]; exists {
		return settings.IsCustom
	}
	return false
}

func defaultTranslate() TranslateConfig {
	primary, secondary := language.Defaults(osLocale())
	return TranslateConfig{PrimaryLanguage: primary, SecondaryLanguage: secondary}
}

func osLocale() string {
	tag, err := locale.GetLocale()
	if err != nil {
		return "en"
	}
	return strings.ReplaceAll(tag, "_", "-")
}

// A hand-edited partial config still starts rather than booting with zero-valued hotkeys and limits.
func (c *Config) applyDefaults() {
	if c.Actions == nil {
		c.Actions = DefaultActions()
	} else {
		defaults := DefaultActions()
		for _, kind := range ActionOrder {
			action, ok := c.Actions[kind]
			if !ok || action.Hotkey == "" {
				c.Actions[kind] = defaults[kind]
			}
		}
	}

	if c.Operations == nil {
		c.Operations = DefaultOperations()
	} else {
		for _, op := range OperationOrder {
			c.Operations[op] = c.operationLocked(op)
		}
	}

	if c.Translate.PrimaryLanguage == "" || c.Translate.SecondaryLanguage == "" {
		c.Translate = defaultTranslate()
	}
	if c.Appearance.Indicators == nil {
		c.Appearance.Indicators = defaultIndicators()
	}
	if c.Appearance.Theme == "" {
		c.Appearance.Theme = defaultAppearance().Theme
	}
	if c.Speech.Engine == "" {
		c.Speech.Engine = defaultSpeech().Engine
	}
	if c.Paste == "" {
		c.Paste = PasteStandard
	}
}
