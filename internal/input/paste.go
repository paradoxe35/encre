package input

import "github.com/paradoxe35/encre/internal/config"

// paster is the slice of the key simulator a paste needs.
type paster interface {
	Paste() error
	PasteTerminal() error
}

// pasteWith sends the chord the setting names. Anything else, including the empty value of a
// config written before the setting existed, is the standard chord.
func pasteWith(sim paster, shortcut config.PasteShortcut) error {
	if shortcut == config.PasteTerminal {
		return sim.PasteTerminal()
	}
	return sim.Paste()
}
