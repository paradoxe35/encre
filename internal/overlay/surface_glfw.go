//go:build linux || darwin || windows

package overlay

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"unsafe"

	"github.com/go-gl/gl/v2.1/gl"
	"github.com/go-gl/glfw/v3.4/glfw"
	"github.com/paradoxe35/encre/internal/logger"
)

// Distance from the bottom edge of the work area.
const bottomMargin = 48

// Supported reports whether this session can float a window. Wayland offers no way to
// place one or keep it above the others without a protocol most desktops lack.
func Supported() bool {
	return glfw.GetPlatform() != glfw.PlatformWayland
}

// The GL function table is process-wide and shared with Fyne's painter.
var initGL sync.Once

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

	screens := attachedScreens()
	if len(screens) == 0 {
		return nil, errors.New("no monitor")
	}

	setHints()
	defer resetHints()

	window, err := glfw.CreateWindow(Width, Height, "Encre", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create the indicator window: %w", err)
	}
	if window == nil {
		return nil, errors.New("the window system refused the indicator window")
	}
	if window.GetAttrib(glfw.TransparentFramebuffer) == glfw.False {
		logger.Warn("Indicator window cannot be transparent here; is a compositor running?")
	}

	// GLFW's hints stop it asking for focus; the window system has its own ideas
	// about new windows, so each platform tells it not to.
	noFocus(window)

	focus, known := focusPoint()
	ww, wh := window.GetSize()
	origin := pillOrigin(workareaFor(screens, focus, known), ww, wh)
	window.SetPos(origin.X, origin.Y)

	window.MakeContextCurrent()
	var glErr error
	initGL.Do(func() { glErr = gl.Init() })
	if glErr != nil {
		window.Destroy()
		return nil, fmt.Errorf("could not initialise OpenGL for the indicator: %w", glErr)
	}
	// Never let the swap wait for vsync: this runs on Fyne's thread.
	glfw.SwapInterval(0)
	glfw.DetachCurrentContext()

	window.Show()

	fw, fh := window.GetFramebufferSize()
	if fw == 0 || fh == 0 {
		window.Destroy()
		return nil, errors.New("the indicator window has no framebuffer yet")
	}
	return &glfwSurface{window: window, width: fw, height: fh}, nil
}

// attachedScreens lists the monitors in GLFW's virtual coordinates, primary first.
func attachedScreens() []screen {
	var screens []screen
	for _, monitor := range glfw.GetMonitors() {
		mode := monitor.GetVideoMode()
		if mode == nil {
			continue
		}
		x, y := monitor.GetPos()
		wx, wy, ww, wh := monitor.GetWorkarea()
		screens = append(screens, screen{
			bounds:   image.Rect(x, y, x+mode.Width, y+mode.Height),
			workarea: image.Rect(wx, wy, wx+ww, wy+wh),
		})
	}
	return screens
}

func setHints() {
	glfw.WindowHint(glfw.Visible, glfw.False)
	glfw.WindowHint(glfw.Decorated, glfw.False)
	glfw.WindowHint(glfw.Resizable, glfw.False)
	glfw.WindowHint(glfw.Floating, glfw.True)
	glfw.WindowHint(glfw.TransparentFramebuffer, glfw.True)
	glfw.WindowHint(glfw.FocusOnShow, glfw.False)
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
