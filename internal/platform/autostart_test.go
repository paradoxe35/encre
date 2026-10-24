package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAnAppImageStartsFromItsFileNotItsMount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("AppImages are Linux only")
	}
	t.Setenv("APPIMAGE", "/home/me/Applications/Encre.AppImage")
	path, err := launchPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/home/me/Applications/Encre.AppImage" {
		t.Fatalf("launch path %q, want the AppImage file", path)
	}
}

func TestLaunchPathIsTheExecutableOutsideAnAppImage(t *testing.T) {
	t.Setenv("APPIMAGE", "")
	path, err := launchPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("empty launch path")
	}
}

func TestAnUnresolvablePathIsKept(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone", "encre")
	if got := resolveSymlinks(missing); got != missing {
		t.Fatalf("resolved to %q, want the original path kept", got)
	}
}

func TestSymlinksAreResolved(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "encre")
	if err := os.WriteFile(target, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	want, _ := filepath.EvalSymlinks(target)
	if got := resolveSymlinks(link); got != want {
		t.Fatalf("resolved to %q, want %q", got, want)
	}
}
