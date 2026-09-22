//go:build linux || darwin || windows

package overlay

import (
	"errors"
	"fmt"
	"image"
	"os"
	"unsafe"

	"github.com/go-gl/gl/v2.1/gl"
	"github.com/go-gl/glfw/v3.4/glfw"
)

// Distance from the bottom edge of the work area.
const bottomMargin = 48

// Supported reports whether this session can float a window. Wayland offers no way to
// place one or keep it above the others without a protocol most desktops lack.
func Supported() bool {
	switch glfw.GetPlatform() {
	case glfw.PlatformWayland:
		return false
	case 0:
		// Not initialised yet, as when the settings are built before the app runs.
		return !waylandSession(os.Getenv)
	default:
		return true
	}
}

// glfwSurface rides on the GLFW instance Fyne already runs: the window is created on
// Fyne's main thread and shown without focus, above everything, letting clicks through.
type glfwSurface struct {
	window *glfw.Window
	width  int
	height int
}

func openSurface() (surface, error) {
	if !Supported() {
		return nil, errors.New("floating windows are not available on Wayland")
	}

	monitor := glfw.GetPrimaryMonitor()
	if monitor == nil {
		return nil, errors.New("no monitor")
	}

	setHints()
	defer resetHints()

	window, err := glfw.CreateWindow(Width, Height, "Encre", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create the indicator window: %w", err)
	}

	x, y, w, h := monitor.GetWorkarea()
	ww, wh := window.GetSize()
	window.SetPos(x+(w-ww)/2, y+h-wh-bottomMargin)

	window.MakeContextCurrent()
	if err := gl.Init(); err != nil {
		window.Destroy()
		return nil, fmt.Errorf("could not initialise OpenGL for the indicator: %w", err)
	}
	// Never let the swap wait for vsync: this runs on Fyne's thread.
	glfw.SwapInterval(0)
	glfw.DetachCurrentContext()

	window.Show()

	fw, fh := window.GetFramebufferSize()
	return &glfwSurface{window: window, width: fw, height: fh}, nil
}

func setHints() {
	glfw.WindowHint(glfw.Visible, glfw.False)
	glfw.WindowHint(glfw.Decorated, glfw.False)
	glfw.WindowHint(glfw.Resizable, glfw.False)
	glfw.WindowHint(glfw.Floating, glfw.True)
	glfw.WindowHint(glfw.TransparentFramebuffer, glfw.True)
	glfw.WindowHint(glfw.FocusOnShow, glfw.False)
	glfw.WindowHint(glfw.Focused, glfw.False)
	glfw.WindowHint(glfw.MousePassthrough, glfw.True)
	glfw.WindowHint(glfw.ScaleToMonitor, glfw.True)
	glfw.WindowHint(glfw.ContextVersionMajor, 2)
	glfw.WindowHint(glfw.ContextVersionMinor, 1)
}

// Fyne sets the hints it cares about before each window, but not these; left
// behind they would make the settings window float and ignore the mouse.
func resetHints() {
	glfw.WindowHint(glfw.Visible, glfw.True)
	glfw.WindowHint(glfw.Decorated, glfw.True)
	glfw.WindowHint(glfw.Resizable, glfw.True)
	glfw.WindowHint(glfw.Floating, glfw.False)
	glfw.WindowHint(glfw.TransparentFramebuffer, glfw.False)
	glfw.WindowHint(glfw.FocusOnShow, glfw.True)
	glfw.WindowHint(glfw.Focused, glfw.True)
	glfw.WindowHint(glfw.MousePassthrough, glfw.False)
	glfw.WindowHint(glfw.ScaleToMonitor, glfw.False)
}

func (s *glfwSurface) Scale() float64 {
	return float64(s.width) / Width
}

// Present blits the premultiplied frame top-down over a transparent clear.
func (s *glfwSurface) Present(frame *image.RGBA) {
	s.window.MakeContextCurrent()
	defer glfw.DetachCurrentContext()

	gl.Viewport(0, 0, int32(s.width), int32(s.height))
	gl.ClearColor(0, 0, 0, 0)
	gl.Clear(gl.COLOR_BUFFER_BIT)

	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.RasterPos2i(-1, 1)
	gl.PixelZoom(1, -1)
	b := frame.Bounds()
	gl.DrawPixels(int32(b.Dx()), int32(b.Dy()), gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&frame.Pix[0]))

	s.window.SwapBuffers()
}

func (s *glfwSurface) Close() {
	s.window.Destroy()
}
