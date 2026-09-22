// Package overlay shows a small floating indicator while dictating. It never takes
// keyboard focus: the words being dictated must keep landing in the user's app.
package overlay

type Phase int

const (
	// Listening shows the voice; Working shows that a model or provider is busy.
	Listening Phase = iota
	Working
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
