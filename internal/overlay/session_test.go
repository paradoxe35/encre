package overlay

import "testing"

func TestTheSessionIsReadLikeFyneReadsIt(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wayland bool
	}{
		{"plain X11", map[string]string{"DISPLAY": ":0"}, false},
		{"pure Wayland", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"Wayland with XWayland", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, false},
		{"forced x11", map[string]string{"FYNE_PLATFORM": "x11", "WAYLAND_DISPLAY": "wayland-0"}, false},
		{"forced wayland", map[string]string{"FYNE_PLATFORM": "wayland", "DISPLAY": ":0"}, true},
		{"headless", map[string]string{}, false},
	}
	for _, c := range cases {
		env := func(key string) string { return c.env[key] }
		if got := waylandSession(env); got != c.wayland {
			t.Errorf("%s: wayland=%v, want %v", c.name, got, c.wayland)
		}
	}
}
