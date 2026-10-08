//go:build linux && wayland

package overlay

import (
	"image"

	"github.com/go-gl/glfw/v3.4/glfw"
)

// A Wayland-only build never opens a surface, so there is nothing to keep unfocused or to place.
func noFocus(*glfw.Window) {}

func focusPoint() (image.Point, bool) { return image.Point{}, false }

func Panel(uintptr, image.Rectangle, Look) {}

func SetOpacity(uintptr, float64) {}

func GlassBackdrop() Backdrop { return BackdropNone }

func Corner(float32) float32 { return 0 }
