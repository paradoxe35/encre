package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecPathsAreQuotedThenEscapedAsDesktopEntryStrings(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`/opt/Encre/encre`, `/opt/Encre/encre`},
		{`/home/me/My Apps/Encre.AppImage`, `"/home/me/My Apps/Encre.AppImage"`},
		{`/home/me/"quoted"/encre`, `"/home/me/\\"quoted\\"/encre"`},
		{`/home/me/$work/Encre.AppImage`, `"/home/me/\\$work/Encre.AppImage"`},
		{"/home/me/`tick`/encre", "\"/home/me/\\\\`tick\\\\`/encre\""},
		{`/home/me/back\slash/encre`, `"/home/me/back\\\\slash/encre"`},
		{`/home/me/100%/Encre.AppImage`, `/home/me/100%%/Encre.AppImage`},
		{`/home/me/100% sure/encre`, `"/home/me/100%% sure/encre"`},
		{`/home/me/it's/encre`, `"/home/me/it's/encre"`},
		{"/home/me/tab\there/encre", `"/home/me/tab\there/encre"`},
	} {
		if got := escapeExecPath(tc.path); got != tc.want {
			t.Errorf("escapeExecPath(%q) = %s, want %s", tc.path, got, tc.want)
		}
	}
}

func TestEnableWritesTheEscapedLaunchPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPIMAGE", `/home/me/$work/100%/Encre.AppImage`)

	a := &autoStart{}
	if err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "autostart", "encre.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "\nExec=\"/home/me/\\\\$work/100%%/Encre.AppImage\"\n"; !strings.Contains(string(data), want) {
		t.Fatalf("desktop file %s lacks %s", data, want)
	}
	if !a.IsEnabled() {
		t.Fatal("not enabled after Enable")
	}
	if err := a.Disable(); err != nil || a.IsEnabled() {
		t.Fatalf("still enabled after Disable: %v", err)
	}
}
