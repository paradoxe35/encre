package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/ai/tools"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/history"
	"github.com/paradoxe35/encre/internal/input"
	"github.com/paradoxe35/encre/internal/language"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/overlay"
	"github.com/paradoxe35/encre/internal/prompt"
	"github.com/paradoxe35/encre/internal/stt"
)

var errTimedOut = errors.New("timed out")

type Processor struct {
	mu               sync.Mutex
	config           *config.Config
	providerFactory  *ai.ProviderFactory
	clipboardManager *input.FFIClipboardManager
	history          *history.Store
	processing       bool
	indicator        overlay.Overlay
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
		indicator:        overlay.Disabled{},
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
		return fmt.Errorf("no AI provider configured - add an API key in Settings > AI")
	}

	if _, err := p.providerNamed(name); err != nil {
		return err
	}

	logger.Info("AI provider initialized", "provider", name)
	return nil
}

func (p *Processor) buildProvider(cfg *config.Config, name string) (ai.Provider, error) {
	apiKey, err := cfg.GetAPIKey(name)
	if err != nil {
		return nil, fmt.Errorf("no API key configured for %s", name)
	}

	settings := cfg.GetProviderSettings(name)
	if settings.RequiresAPIKey() && strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("no API key configured for %s", name)
	}

	return ai.FromSettings(name, settings, apiKey, cfg.IsCustomProvider(name))
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

// SetOverlay swaps the indicator; the old one is hidden in case it was showing.
func (p *Processor) SetOverlay(indicator overlay.Overlay) {
	p.mu.Lock()
	previous := p.indicator
	p.indicator = indicator
	p.mu.Unlock()
	if previous != nil {
		previous.Hide()
	}
}

func (p *Processor) overlay() overlay.Overlay {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.indicator == nil {
		return overlay.Disabled{}
	}
	return p.indicator
}

// Two overlapping runs would fight over the clipboard, which each saves and restores.
func (p *Processor) begin() (func(), error) {
	p.mu.Lock()
	if p.processing {
		p.mu.Unlock()
		logger.Warn("Already processing an action")
		return nil, fmt.Errorf("another action is already running")
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

	indicator := p.overlay()
	indicator.Show(overlay.Thinking)
	defer indicator.Hide()

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

	if err := p.clipboardManager.ReplaceSelectedText(result.text, p.currentConfig().PasteShortcut()); err != nil {
		return fmt.Errorf("failed to replace text: %w", err)
	}

	p.recordHistory(kind, text, result)
	logger.Info("Action completed", "action", kind)
	return nil
}

func (p *Processor) History() *history.Store {
	return p.history
}

// Raw and final differ when the AI cleanup ran; showing both is what makes the history useful.
func (p *Processor) RecordDictation(raw, final string) {
	model := ""
	if m, ok := stt.FindModel(p.currentConfig().SpeechSettings().ModelID); ok {
		model = m.Name
	}

	p.history.Add(history.Entry{
		Kind:       history.KindDictate,
		Original:   raw,
		Result:     final,
		Model:      model,
		Characters: utf8.RuneCountInString(final),
	})
}

// Never blocks the caller: history is a convenience, not a dependency.
func (p *Processor) recordHistory(kind config.ActionKind, original string, result reply) {
	cfg := p.currentConfig()

	entry := history.Entry{
		Kind:       history.Kind(kind.Operation()),
		Original:   original,
		Result:     result.text,
		Provider:   result.provider,
		Model:      result.model,
		Characters: utf8.RuneCountInString(result.text),
	}
	if kind.Operation() == config.OpTranslate {
		translate := cfg.Translation()
		entry.FromLang = language.Find(translate.PrimaryLanguage).Name
		entry.ToLang = language.Find(translate.SecondaryLanguage).Name
	}
	p.history.Add(entry)
}

type reply struct {
	text     string
	provider string
	model    string
}

func (p *Processor) transform(text string, kind config.ActionKind) (reply, error) {
	cfg := p.currentConfig()
	mentioned, source := p.parseProviderMention(cfg, text)
	if strings.TrimSpace(source) == "" {
		return reply{}, fmt.Errorf("nothing to work with - the selection is empty")
	}

	result, err := p.complete(context.Background(), cfg, request{op: kind.Operation(), mentioned: mentioned, text: source})
	if err != nil {
		return reply{}, err
	}

	result.text = ai.CleanResponse(result.text)
	if result.text == "" {
		return reply{}, fmt.Errorf("the model returned an empty result")
	}

	// The reply replaces the selection as it was, so the selection's own edges go back on. Without
	// them "word " returns as "word" and runs into the next one.
	result.text = leadingWhitespace(source) + result.text + trailingWhitespace(source)
	return result, nil
}

// Ask's answer is shown, not pasted, so its formatting stays.
func (p *Processor) Ask(ctx context.Context, question string, onText, onStatus func(string)) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", ErrNoSpeech
	}

	cfg := p.currentConfig()
	result, err := p.complete(ctx, cfg, request{
		op:       config.OpAsk,
		text:     question,
		past:     p.remembered(cfg),
		tools:    askTools(cfg.Operation(config.OpAsk)),
		onText:   onText,
		onStatus: onStatus,
	})
	if err != nil {
		return "", err
	}

	answer := strings.TrimSpace(result.text)
	if answer == "" {
		return "", fmt.Errorf("the model returned an empty answer")
	}

	p.history.Add(history.Entry{
		Kind:       history.KindAsk,
		Original:   question,
		Result:     answer,
		Provider:   result.provider,
		Model:      result.model,
		Characters: utf8.RuneCountInString(answer),
	})
	return answer, nil
}

type request struct {
	op        config.Operation
	mentioned string
	text      string
	past      []ai.Turn
	tools     []tools.Tool
	// onText streams the reply; without it the whole reply is waited for.
	onText   func(string)
	onStatus func(string)
}

func (p *Processor) complete(ctx context.Context, cfg *config.Config, req request) (reply, error) {
	op := req.op
	operation := cfg.Operation(op)
	trimmed := strings.TrimSpace(req.text)

	if err := checkCharacterLimit(trimmed, operation.CharacterLimit); err != nil {
		return reply{}, err
	}

	name, provider, err := p.resolveProvider(cfg, op, req.mentioned)
	if err != nil {
		return reply{}, err
	}

	// The timeout is a silence: a stream that keeps writing is given as long as it needs.
	timeout := time.Duration(operation.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	silence := time.AfterFunc(timeout, func() { cancel(errTimedOut) })
	defer silence.Stop()

	logger.Info("Sending text to AI provider",
		"operation", op,
		"provider", name,
		"model", provider.Model(),
		"characters", utf8.RuneCountInString(trimmed),
		"remembered", len(req.past),
		"tools", len(req.tools),
		"streaming", req.onText != nil,
	)

	prompt := ai.Prompt{System: systemPrompt(cfg, op, operation), History: req.past, Text: trimmed}
	if op == config.OpAsk {
		prompt.Context = askContext(time.Now())
	}
	keepAlive := func() { silence.Reset(timeout) }
	answer, err := generate(ctx, provider, prompt, req, keepAlive)
	if errors.Is(context.Cause(ctx), errTimedOut) {
		return reply{}, fmt.Errorf("no reply within %ds - raise the timeout in Settings > Actions > %s",
			operation.TimeoutSeconds, op.Label())
	}
	if err != nil {
		return reply{}, err
	}
	return reply{text: answer, provider: name, model: provider.Model()}, nil
}

func generate(ctx context.Context, provider ai.Provider, prompt ai.Prompt, req request, keepAlive func()) (string, error) {
	if req.onText == nil {
		return provider.Complete(ctx, prompt)
	}
	onText := func(text string) {
		keepAlive()
		req.onText(text)
	}
	model, canUseTools := provider.(ai.ToolUser)
	if !canUseTools || len(req.tools) == 0 {
		return provider.Stream(ctx, prompt, onText)
	}
	return converse(ctx, model, prompt, req.tools, onText, req.onStatus, keepAlive)
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
func (p *Processor) resolveProvider(cfg *config.Config, op config.Operation, mentioned string) (string, ai.Provider, error) {
	if mentioned != "" {
		provider, err := p.providerNamed(mentioned)
		if err == nil {
			logger.Info("Using mentioned provider", "provider", mentioned)
			return mentioned, provider, nil
		}
		logger.Warn("Mentioned provider unusable, falling back",
			"mentioned", mentioned, "error", err)
	}

	name := cfg.ProviderFor(op)
	provider, err := p.providerNamed(name)
	if err != nil {
		return "", nil, fmt.Errorf("no AI provider configured - add an API key in Settings > AI")
	}
	return name, provider, nil
}

func (p *Processor) IsProcessing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.processing
}

func (p *Processor) Close() {
	p.clipboardManager.Close()
}

// Without a usable mention the text comes back untouched, so the whitespace it carried is kept.
func (p *Processor) parseProviderMention(cfg *config.Config, text string) (provider, remainder string) {
	if !cfg.ProviderMentionsEnabled() {
		return "", text
	}

	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "@") {
		return "", text
	}

	mention, rest, _ := strings.Cut(trimmed, " ")
	name, found := findProvider(cfg, strings.TrimPrefix(mention, "@"))
	if !found {
		return "", text
	}

	return name, strings.TrimSpace(rest)
}

// Matches case-insensitively and returns the stored spelling.
func findProvider(cfg *config.Config, name string) (string, bool) {
	for stored := range cfg.AIProvider.Providers {
		if strings.EqualFold(stored, name) {
			return stored, true
		}
	}
	return "", false
}

// Counts characters, not bytes: an accented letter is two bytes in UTF-8, and len() would
// halve the limit for exactly the text this app corrects.
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

// SaveCurrent first so the user's clipboard survives.
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
	return p.clipboardManager.ReplaceSelectedText(text, p.currentConfig().PasteShortcut())
}

// Uses the dedicated dictation prompt rather than an editable action prompt, so unrelated
// instructions cannot change the task.
func (p *Processor) CleanTranscript(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", nil
	}

	cfg := p.currentConfig()
	provider, err := p.providerNamed(cfg.ProviderFor(config.OpDictate))
	if err != nil {
		return "", fmt.Errorf("transcript cleanup unavailable: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(config.DefaultTimeoutSeconds)*time.Second)
	defer cancel()

	logger.Info("Cleaning dictated transcript",
		"provider", provider.Name(),
		"model", provider.Model(),
		"characters", utf8.RuneCountInString(trimmed),
	)

	cleaned, err := provider.Complete(ctx, ai.Prompt{System: prompt.Dictate, Text: trimmed})
	if err != nil {
		return "", fmt.Errorf("transcript cleanup failed: %w", err)
	}

	cleaned = ai.CleanResponse(cleaned)
	if cleaned == "" {
		return "", fmt.Errorf("transcript cleanup returned empty text")
	}
	return cleaned, nil
}
