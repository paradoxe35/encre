package input

import "github.com/paradoxe35/encre/internal/config"

type paster interface {
	Paste() error
	PasteTerminal() error
}

// Anything else, including empty, is the standard chord.
func pasteWith(sim paster, shortcut config.PasteShortcut) error {
	if shortcut == config.PasteTerminal {
		return sim.PasteTerminal()
	}
	return sim.Paste()
}
