//go:build linux && wayland

package overlay

import "github.com/go-gl/glfw/v3.4/glfw"

// A Wayland-only build never opens a surface, so there is nothing to keep unfocused.
func noFocus(*glfw.Window) {}
