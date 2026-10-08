// Package overlay shows a small floating indicator while dictating. It never takes
// keyboard focus: the words being dictated must keep landing in the user's app.
package overlay

type Phase int

const (
	// Listening shows the voice, Transcribing that speech is being turned into
	// text, Thinking that a provider is rewriting text.
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

// Look is how a panel is drawn: its corner radius in window pixels, and whether it is glass, which
// shows the desktop through it and blurs it where the platform can.
type Look struct {
	Radius int
	Glass  bool
}

// Backdrop is what shows through a glass panel.
type Backdrop int

const (
	// BackdropNone is for a desktop that cannot show through a window, whose glass must be opaque.
	BackdropNone Backdrop = iota
	BackdropSharp
	BackdropBlurred
	// BackdropFrosted is a desktop that cannot blur behind a window, but whose screen can be read:
	// the panel stays opaque and draws the screen behind it blurred, from Frosted.
	BackdropFrosted
)
