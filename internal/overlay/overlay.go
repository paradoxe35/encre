// Package overlay floats a dictation indicator that never takes keyboard focus.
package overlay

type Phase int

const (
	// Thinking means a provider is rewriting text.
	Listening Phase = iota
	Transcribing
	Thinking
)

type Overlay interface {
	Show(Phase)
	// Level is the microphone RMS, 0 to 1, from any thread and at any rate.
	Level(float32)
	Hide()
}

// Disabled is the overlay while the setting is off or the platform cannot float a window.
type Disabled struct{}

func (Disabled) Show(Phase)    {}
func (Disabled) Level(float32) {}
func (Disabled) Hide()         {}

// Look is how a panel is drawn; Radius is in window pixels.
type Look struct {
	Radius int
	Glass  bool
}

// Backdrop is what shows through a glass panel.
type Backdrop int

const (
	// BackdropNone: the desktop cannot show through a window, so glass must be opaque.
	BackdropNone Backdrop = iota
	BackdropSharp
	BackdropBlurred
	// BackdropFrosted: the panel stays opaque and draws the screen behind it blurred, from Frosted.
	BackdropFrosted
)
