package revision

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/history"
	"github.com/paradoxe35/encre/internal/input"
	"github.com/paradoxe35/encre/internal/language"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/prompt"
	"github.com/paradoxe35/encre/internal/stt"
)

// Processor runs the text actions: capture a selection, send it to a provider,
// write the result back where it came from.
type Processor struct {
	mu               sync.Mutex
	config           *config.Config
	providerFactory  *ai.ProviderFactory
	clipboardManager *input.FFIClipboardManager
	history          *history.Store
	processing       bool
}

func NewProcessor(cfg *config.Config) (*Processor, error) {
	clipManager, err := input.NewFFIClipboardManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create clipboard manager: %w", err)
	}

	p := &Processor{
		config:           cfg,
		providerFactory:  ai.NewProviderFactory(),
		clipboardManager: clipManager,
		history:          history.NewStore(),
	}

	if err := p.initializeProviders(); err != nil {
		logger.Warn("AI provider not configured at startup", "error", err)
	}

	config.RegisterListener(func(newCfg *config.Config) {
		p.mu.Lock()
		p.config = newCfg
		p.mu.Unlock()

		p.providerFactory.Reset()
		if err := p.initializeProviders(); err != nil {
			logger.Warn("AI provider not configured", "error", err)
		}
	})

	return p, nil
}

func (p *Processor) currentConfig() *config.Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config
}

func (p *Processor) initializeProviders() error {
	cfg := p.currentConfig()

	name := cfg.GetCurrentProvider()
	if name == "" {
		return fmt.Errorf("no provider configured")
	}

	provider, err := p.buildProvider(cfg, name)
	if err != nil {
		return err
	}

	p.providerFactory.Register(name, provider)
	p.providerFactory.SetCurrent(name)

	logger.Info("AI provider initialized", "provider", name)
	return nil
}

// buildProvider is the single place a provider is constructed from settings.
func (p *Processor) buildProvider(cfg *config.Config, name string) (ai.Provider, error) {
	apiKey, err := cfg.GetAPIKey(name)
	if err != nil {
		return nil, fmt.Errorf("no API key configured for %s", name)
	}

	settings := cfg.GetProviderSettings(name)
	if settings.RequiresAPIKey() && strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("no API key configured for %s", name)
	}

	var provider ai.Provider
	switch {
	case settings.IsCustom:
		provider, err = ai.NewCustomProvider(name, settings.ProviderType, apiKey,
			settings.BaseURL, settings.Model, settings.Temperature)
		if err != nil {
			return nil, fmt.Errorf("failed to create custom provider: %w", err)
		}
	case name == config.BuiltInOpenAI:
		provider = ai.NewOpenAIProvider(apiKey, settings.BaseURL, settings.Model, settings.Temperature)
	case name == config.BuiltInClaude:
		provider = ai.NewAnthropicProvider(apiKey, settings.BaseURL, settings.Model, settings.Temperature)
	case name == config.BuiltInGemini:
		provider = ai.NewGeminiProvider(apiKey, settings.BaseURL, settings.Model, settings.Temperature)
	default:
		return nil, fmt.Errorf("unknown provider: %s", name)
	}

	if aware, ok := provider.(ai.ReasoningAware); ok {
		aware.SetLowReasoning(settings.LowReasoning)
	}
	return provider, nil
}

func (p *Processor) providerNamed(name string) (ai.Provider, error) {
	if provider, err := p.providerFactory.Get(name); err == nil {
		return provider, nil
	}

	provider, err := p.buildProvider(p.currentConfig(), name)
	if err != nil {
		return nil, err
	}

	p.providerFactory.Register(name, provider)
	return provider, nil
}

// begin takes the one-at-a-time guard. Two overlapping runs would fight over the clipboard, a
// single global resource each of them saves and restores.
func (p *Processor) begin() (func(), error) {
	p.mu.Lock()
	if p.processing {
		p.mu.Unlock()
		logger.Warn("Already processing an action")
		return nil, fmt.Errorf("already processing, please wait")
	}
	p.processing = true
	p.mu.Unlock()

	return func() {
		p.mu.Lock()
		p.processing = false
		p.mu.Unlock()
	}, nil
}

func outcomeError(outcome input.CaptureOutcome) error {
	switch outcome {
	case input.CaptureNothingSelected:
		return fmt.Errorf("no text selected")
	case input.CaptureCopyFailed:
		return fmt.Errorf("could not copy from that window - some applications block it")
	default:
		return nil
	}
}

// Run captures text for the action, transforms it, and writes it back.
func (p *Processor) Run(kind config.ActionKind) error {
	release, err := p.begin()
	if err != nil {
		return err
	}
	defer release()

	selectAll := kind.SelectsAll()
	logger.Info("Action started", "action", kind)

	capture := p.clipboardManager.CaptureSelection
	if selectAll {
		capture = p.clipboardManager.CaptureAll
	}

	text, outcome, err := capture()
	if err != nil {
		return err
	}
	if err := outcomeError(outcome); err != nil {
		return err
	}

	result, err := p.transform(text, kind)
	if err != nil {
		p.clipboardManager.Abandon()
		return err
	}

	// Select again: a field can drop its selection while the model is working, and pasting
	// without one inserts the result beside the original instead of replacing it.
	if selectAll {
		if err := input.FFISimulateSelectAll(); err != nil {
			p.clipboardManager.Abandon()
			return fmt.Errorf("failed to select all for paste: %w", err)
		}
	}

	if err := p.clipboardManager.ReplaceSelectedText(result); err != nil {
		return fmt.Errorf("failed to replace text: %w", err)
	}

	p.recordHistory(kind, text, result, "")
	logger.Info("Action completed", "action", kind)
	return nil
}

func (p *Processor) History() *history.Store {
	return p.history
}

// RecordSpeech stores a finished dictation. Raw and final differ when the
// AI cleanup pass ran; showing both is what makes the history useful.
func (p *Processor) RecordSpeech(raw, final string) {
	model := ""
	if m, ok := stt.FindModel(p.currentConfig().Speech.ModelID); ok {
		model = m.Name
	}

	p.history.Add(history.Entry{
		Kind:       history.KindSpeech,
		Original:   raw,
		Result:     final,
		Model:      model,
		Characters: utf8.RuneCountInString(final),
	})
}

// recordHistory stores a finished action. It never blocks the caller: history
// is a convenience, not a dependency.
func (p *Processor) recordHistory(kind config.ActionKind, original, result, model string) {
	cfg := p.currentConfig()

	entry := history.Entry{
		Kind:       history.Kind(kind.Operation()),
		Original:   original,
		Result:     result,
		Provider:   cfg.GetCurrentProvider(),
		Model:      model,
		Characters: utf8.RuneCountInString(result),
	}
	if kind.Operation() == config.OpTranslate {
		translate := cfg.Translation()
		entry.FromLang = language.Find(translate.PrimaryLanguage).Name
		entry.ToLang = language.Find(translate.SecondaryLanguage).Name
	}
	p.history.Add(entry)
}

func (p *Processor) transform(text string, kind config.ActionKind) (string, error) {
	cfg := p.currentConfig()
	operation := cfg.Operation(kind.Operation())

	mentioned, cleanedText, hasMention := p.parseProviderMention(cfg, text)

	source := text
	if hasMention {
		source = cleanedText
	}

	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return "", fmt.Errorf("nothing to work with - the selection is empty")
	}

	if err := checkCharacterLimit(trimmed, operation.CharacterLimit); err != nil {
		return "", err
	}

	provider, err := p.resolveProvider(cfg, kind.Operation(), mentioned)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(operation.TimeoutSeconds)*time.Second)
	defer cancel()

	logger.Info("Sending text to AI provider",
		"action", kind,
		"provider", provider.GetName(),
		"model", provider.GetModel(),
		"characters", utf8.RuneCountInString(trimmed),
	)

	answer, err := provider.ReviseText(ctx, trimmed, systemPrompt(cfg, kind.Operation(), operation))
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", kind.Label(), err)
	}

	cleaned := ai.CleanResponse(answer)
	if cleaned == "" {
		return "", fmt.Errorf("the model returned an empty result")
	}

	// The reply replaces the selection as it was, so the selection's own edges go back on. Without
	// them "word " returns as "word" and runs into the next one.
	return leadingWhitespace(source) + cleaned + trailingWhitespace(source), nil
}

func systemPrompt(cfg *config.Config, op config.Operation, operation config.OperationConfig) string {
	template := operation.PromptOrDefault(op)
	if op != config.OpTranslate {
		return template
	}

	translate := cfg.Translation()
	return prompt.RenderTranslate(template,
		language.Find(translate.PrimaryLanguage).Name,
		language.Find(translate.SecondaryLanguage).Name,
	)
}

// resolveProvider prefers an @mention, then the action's own override, then the
// default. A failed mention falls back rather than aborting the run.
func (p *Processor) resolveProvider(cfg *config.Config, op config.Operation, mentioned string) (ai.Provider, error) {
	if mentioned != "" {
		provider, err := p.providerNamed(mentioned)
		if err == nil {
			logger.Info("Using mentioned provider", "provider", mentioned)
			return provider, nil
		}
		logger.Warn("Mentioned provider unusable, falling back",
			"mentioned", mentioned, "error", err)
	}

	provider, err := p.providerNamed(cfg.ProviderFor(op))
	if err != nil {
		return nil, fmt.Errorf("no AI provider configured - add an API key in Settings")
	}
	return provider, nil
}

func (p *Processor) IsProcessing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.processing
}

func (p *Processor) Close() {
	if p.clipboardManager != nil {
		p.clipboardManager.Close()
	}
}

// parseProviderMention strips a leading "@provider" and reports which provider
// it named, so a selection can opt into a provider for one run.
func (p *Processor) parseProviderMention(cfg *config.Config, text string) (provider, remainder string, ok bool) {
	if !cfg.ProviderMentionsEnabled() {
		return "", text, false
	}

	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "@") {
		return "", text, false
	}

	mention, rest, _ := strings.Cut(trimmed, " ")
	name, found := findProvider(cfg, strings.TrimPrefix(mention, "@"))
	if !found {
		return "", text, false
	}

	return name, strings.TrimSpace(rest), true
}

// findProvider matches case-insensitively and returns the stored spelling.
func findProvider(cfg *config.Config, name string) (string, bool) {
	for stored := range cfg.AIProvider.Providers {
		if strings.EqualFold(stored, name) {
			return stored, true
		}
	}
	return "", false
}

// checkCharacterLimit counts in characters, not bytes: an accented letter is two bytes in UTF-8,
// so len() halved the limit for exactly the text this app exists to correct.
func checkCharacterLimit(text string, limit int) error {
	if characters := utf8.RuneCountInString(text); characters > limit {
		return fmt.Errorf("selection is %d characters, over the %d limit", characters, limit)
	}
	return nil
}

func leadingWhitespace(text string) string {
	return text[:len(text)-len(strings.TrimLeftFunc(text, unicode.IsSpace))]
}

func trailingWhitespace(text string) string {
	return text[len(strings.TrimRightFunc(text, unicode.IsSpace)):]
}

// InsertText types text at the cursor without replacing a selection, for
// dictation. SaveCurrent first so the user's clipboard survives.
func (p *Processor) InsertText(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	release, err := p.begin()
	if err != nil {
		return err
	}
	defer release()

	if err := p.clipboardManager.SaveCurrent(); err != nil {
		logger.Warn("Could not save the clipboard before dictating", "error", err)
	}
	return p.clipboardManager.ReplaceSelectedText(text)
}

// CleanTranscript tidies dictated text with the provider selected in the AI
// tab, using the dedicated dictation prompt rather than an editable action
// prompt, so unrelated instructions cannot change the task.
func (p *Processor) CleanTranscript(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", nil
	}

	cfg := p.currentConfig()
	provider, err := p.providerNamed(cfg.GetCurrentProvider())
	if err != nil {
		return "", fmt.Errorf("transcript cleanup unavailable: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(config.DefaultTimeoutSeconds)*time.Second)
	defer cancel()

	logger.Info("Cleaning dictated transcript",
		"provider", provider.GetName(),
		"model", provider.GetModel(),
		"characters", utf8.RuneCountInString(trimmed),
	)

	cleaned, err := provider.ReviseText(ctx, trimmed, prompt.Dictate)
	if err != nil {
		return "", fmt.Errorf("transcript cleanup failed: %w", err)
	}

	cleaned = ai.CleanResponse(cleaned)
	if cleaned == "" {
		return "", fmt.Errorf("transcript cleanup returned empty text")
	}
	return cleaned, nil
}
