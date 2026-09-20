package config

import (
	"runtime"

	"github.com/paradoxe35/encre/internal/prompt"
)

// Operation is what happens to the text. Prompt, provider and limits are configured per
// operation, so two shortcuts that both revise cannot drift apart.
type Operation string

const (
	OpRevise    Operation = "revise"
	OpTranslate Operation = "translate"
	OpDictate   Operation = "dictate"
)

var OperationOrder = []Operation{OpRevise, OpTranslate, OpDictate}

var operationLabels = map[Operation]string{
	OpRevise:    "Revise",
	OpTranslate: "Translate",
	OpDictate:   "Dictate",
}

func (o Operation) Label() string {
	if label, ok := operationLabels[o]; ok {
		return label
	}
	return string(o)
}

func (o Operation) UsesAI() bool { return o != OpDictate }

// ActionKind identifies a binding: an operation plus the text it acts on.
type ActionKind string

const (
	ActionReviseSelection ActionKind = "revise_selection"
	ActionReviseAll       ActionKind = "revise_all"
	ActionTranslate       ActionKind = "translate"
	ActionDictate         ActionKind = "dictate"
)

var ActionOrder = []ActionKind{
	ActionReviseSelection,
	ActionReviseAll,
	ActionTranslate,
	ActionDictate,
}

var actionLabels = map[ActionKind]string{
	ActionReviseSelection: "Revise selection",
	ActionReviseAll:       "Revise everything",
	ActionTranslate:       "Translate selection",
	ActionDictate:         "Dictate",
}

func (k ActionKind) Label() string {
	if label, ok := actionLabels[k]; ok {
		return label
	}
	return string(k)
}

func (k ActionKind) Operation() Operation {
	switch k {
	case ActionTranslate:
		return OpTranslate
	case ActionDictate:
		return OpDictate
	default:
		return OpRevise
	}
}

func (k ActionKind) UsesAI() bool { return k.Operation().UsesAI() }

func (k ActionKind) SelectsAll() bool { return k == ActionReviseAll }

type ActionConfig struct {
	Enabled bool   `json:"enabled"`
	Hotkey  string `json:"hotkey"`

	// PushToTalk records while the hotkey is held instead of toggling on press.
	PushToTalk bool `json:"push_to_talk,omitempty"`
}

type OperationConfig struct {
	SystemPrompt   string `json:"system_prompt,omitempty"`
	CharacterLimit int    `json:"character_limit,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`

	// Empty means the default provider, so changing the default carries every operation with it.
	ProviderID string `json:"provider_id,omitempty"`
}

// A blank prompt means the built-in, so Reset is clearing the field.
func (o OperationConfig) PromptOrDefault(op Operation) string {
	if o.SystemPrompt != "" {
		return o.SystemPrompt
	}
	return defaultPrompt(op)
}

func DefaultPrompt(op Operation) string { return defaultPrompt(op) }

func defaultPrompt(op Operation) string {
	switch op {
	case OpTranslate:
		return prompt.Translate
	case OpDictate:
		return prompt.Dictate
	default:
		return prompt.Revise
	}
}

func defaultHotkeys() map[ActionKind]string {
	switch runtime.GOOS {
	case "darwin":
		return map[ActionKind]string{
			ActionReviseSelection: "ctrl+cmd",
			ActionReviseAll:       "ctrl+option+space",
			ActionTranslate:       "ctrl+option+g",
			ActionDictate:         "ctrl+shift+space",
		}
	case "windows":
		return map[ActionKind]string{
			ActionReviseSelection: "ctrl+win",
			ActionReviseAll:       "ctrl+alt+space",
			ActionTranslate:       "ctrl+alt+g",
			ActionDictate:         "ctrl+shift+space",
		}
	default:
		return map[ActionKind]string{
			ActionReviseSelection: "ctrl+super",
			ActionReviseAll:       "ctrl+alt+space",
			ActionTranslate:       "ctrl+alt+g",
			ActionDictate:         "ctrl+shift+space",
		}
	}
}

// Translate and dictate start off: translate needs a language pair and dictate
// needs a downloaded model, so neither should claim a shortcut unasked.
func enabledByDefault(kind ActionKind) bool {
	return kind == ActionReviseSelection || kind == ActionReviseAll
}

func DefaultActions() map[ActionKind]ActionConfig {
	hotkeys := defaultHotkeys()
	actions := make(map[ActionKind]ActionConfig, len(ActionOrder))

	for _, kind := range ActionOrder {
		actions[kind] = ActionConfig{
			Enabled:    enabledByDefault(kind),
			Hotkey:     hotkeys[kind],
			PushToTalk: kind == ActionDictate,
		}
	}
	return actions
}

func DefaultOperations() map[Operation]OperationConfig {
	operations := make(map[Operation]OperationConfig, len(OperationOrder))
	for _, op := range OperationOrder {
		operations[op] = OperationConfig{
			CharacterLimit: DefaultCharacterLimit,
			TimeoutSeconds: DefaultTimeoutSeconds,
		}
	}
	return operations
}

func (c *Config) Action(kind ActionKind) ActionConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if action, ok := c.Actions[kind]; ok {
		return action
	}
	return DefaultActions()[kind]
}

func (c *Config) SetAction(kind ActionKind, action ActionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Actions == nil {
		c.Actions = make(map[ActionKind]ActionConfig, len(ActionOrder))
	}
	c.Actions[kind] = action
}

func (c *Config) Operation(op Operation) OperationConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.operationLocked(op)
}

func (c *Config) operationLocked(op Operation) OperationConfig {
	operation, ok := c.Operations[op]
	if !ok {
		return DefaultOperations()[op]
	}
	if operation.CharacterLimit == 0 {
		operation.CharacterLimit = DefaultCharacterLimit
	}
	if operation.TimeoutSeconds == 0 {
		operation.TimeoutSeconds = DefaultTimeoutSeconds
	}
	return operation
}

func (c *Config) SetOperation(op Operation, operation OperationConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Operations == nil {
		c.Operations = make(map[Operation]OperationConfig, len(OperationOrder))
	}
	c.Operations[op] = operation
}

func (c *Config) ProviderFor(op Operation) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if id := c.operationLocked(op).ProviderID; id != "" {
		if _, ok := c.AIProvider.Providers[id]; ok {
			return id
		}
	}
	return c.AIProvider.Provider
}
