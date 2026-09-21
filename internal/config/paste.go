package config

// PasteShortcut is the chord that inserts a result over the selection.
type PasteShortcut string

const (
	PasteStandard PasteShortcut = "standard"
	// PasteTerminal is Ctrl+Shift+V, which terminals bind where Ctrl+V is not paste.
	PasteTerminal PasteShortcut = "terminal"
)

func (c *Config) PasteShortcut() PasteShortcut {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Paste
}

func (c *Config) SetPasteShortcut(shortcut PasteShortcut) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Paste = shortcut
}
