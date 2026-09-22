package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	updater "github.com/paradoxe35/go-updater"
)

const testAsset = "encre-linux-amd64.tar.gz"

// staticUpdater serves the given release tags from memory to a 1.0.0 Linux binary install.
func staticUpdater(t *testing.T, tags ...string) *updater.Updater {
	t.Helper()

	releases := make([]*updater.Release, 0, len(tags))
	for _, tag := range tags {
		releases = append(releases, &updater.Release{Tag: tag, Assets: []updater.Asset{{Name: testAsset}}})
	}

	u, err := updater.New(updater.Config{
		Repo:    "encre",
		Version: "1.0.0",
		Source:  updater.StaticSource{Releases: releases},
		Target:  &updater.Target{OS: "linux", Arch: "amd64", Kind: updater.Binary, Path: filepath.Join(t.TempDir(), "encre")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func newTestPanel(t *testing.T, tags ...string) *updatePanel {
	t.Helper()
	test.NewApp()
	return newUpdatePanel(staticUpdater(t, tags...))
}

func TestProgressText(t *testing.T) {
	cases := []struct {
		name     string
		progress updater.Progress
		want     string
	}{
		{"download with a known size", updater.Progress{Stage: updater.Downloading, Downloaded: 12_400_000, Total: 40_000_000}, "Downloading 12 MB of 40 MB"},
		{"download rounds up", updater.Progress{Stage: updater.Downloading, Downloaded: 39_600_000, Total: 40_000_000}, "Downloading 40 MB of 40 MB"},
		{"download of an unknown size", updater.Progress{Stage: updater.Downloading, Downloaded: 3_000_000}, "Downloading 3 MB"},
		{"download not started", updater.Progress{Stage: updater.Downloading}, "Downloading"},
		{"checking", updater.Progress{Stage: updater.Checking}, "Checking for updates"},
		{"verifying", updater.Progress{Stage: updater.Verifying}, "Verifying"},
		{"installing", updater.Progress{Stage: updater.Installing}, "Installing"},
		{"relaunching", updater.Progress{Stage: updater.Relaunching}, "Restarting"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := progressText(tc.progress); got != tc.want {
				t.Errorf("progressText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUpdateStateTransitions(t *testing.T) {
	s := newUpdateState()
	if s.busy() || s.canUpdate() {
		t.Fatal("a fresh state has nothing to update and nothing running")
	}

	s.checking()
	if !s.busy() || s.canUpdate() {
		t.Fatal("checking should lock both buttons")
	}

	s.upToDate()
	if s.busy() || s.canUpdate() || s.release != nil || s.status != "Encre is up to date" {
		t.Fatalf("up to date should settle with no release, got %+v", s)
	}

	rel := &updater.Release{Tag: "v2.0.0"}
	s.found(rel)
	if !s.canUpdate() || s.status != "Version v2.0.0 is available" {
		t.Fatalf("a found release should offer the update, got %+v", s)
	}

	s.checkFailed(errors.New("offline"))
	if !s.failed || !s.canUpdate() || s.release != rel {
		t.Fatalf("a failed re-check should keep the known release, got %+v", s)
	}

	s.updating(updater.Progress{Stage: updater.Downloading, Downloaded: 12_000_000, Total: 40_000_000})
	if !s.busy() || s.canUpdate() || s.failed || s.status != "Downloading 12 MB of 40 MB" {
		t.Fatalf("updating should show progress and lock the buttons, got %+v", s)
	}

	s.updateFailed(errors.New("checksum mismatch"))
	if !s.failed || !s.canUpdate() || s.status != "Update failed: checksum mismatch" {
		t.Fatalf("a failed update should show the error and allow a retry, got %+v", s)
	}
}

func TestUpdatePanelCheckFindsRelease(t *testing.T) {
	p := newTestPanel(t, "v1.0.0", "v2.0.0")

	p.state.checking()
	p.render()
	if !p.checkButton.Disabled() {
		t.Fatal("the check button should be locked while checking")
	}
	p.check()

	if p.status.Text != "Version v2.0.0 is available" {
		t.Fatalf("status = %q", p.status.Text)
	}
	if !p.updateButton.Visible() || p.updateButton.Disabled() || p.checkButton.Disabled() {
		t.Fatal("a found release should show an enabled Update button and free the check button")
	}
}

func TestUpdatePanelCheckUpToDate(t *testing.T) {
	p := newTestPanel(t, "v1.0.0")

	p.check()

	if p.status.Text != "Encre is up to date" {
		t.Fatalf("status = %q", p.status.Text)
	}
	if p.updateButton.Visible() {
		t.Fatal("nothing to update, so no Update button")
	}
}

func TestUpdatePanelCheckFailureIsShown(t *testing.T) {
	p := newTestPanel(t)

	p.check()

	if !strings.HasPrefix(p.status.Text, "Could not check for updates: ") || p.status.Importance != widget.DangerImportance {
		t.Fatalf("status = %q with importance %v", p.status.Text, p.status.Importance)
	}
	if p.checkButton.Disabled() {
		t.Fatal("a failed check should free the button for another try")
	}
}

// The static release carries no checksums.txt, so the update is refused before anything is installed.
func TestUpdatePanelUpdateFailureReenablesButton(t *testing.T) {
	p := newTestPanel(t, "v2.0.0")
	p.check()

	p.runUpdate()
	if !p.updateButton.Disabled() || !p.checkButton.Disabled() || p.status.Text != "Downloading" {
		t.Fatalf("starting an update should lock the buttons, got %q", p.status.Text)
	}
	p.install(p.state.release)

	if !strings.HasPrefix(p.status.Text, "Update failed: ") || p.status.Importance != widget.DangerImportance {
		t.Fatalf("status = %q with importance %v", p.status.Text, p.status.Importance)
	}
	if p.updateButton.Disabled() || !p.updateButton.Visible() || p.checkButton.Disabled() {
		t.Fatal("a failed update should let the user try again")
	}
}

func TestSetAvailableUpdateReachesStatusBarAndPanel(t *testing.T) {
	p := newTestPanel(t)
	w := &MainWindow{statusBinding: binding.NewString(), updates: p}

	w.SetAvailableUpdate(&updater.Release{Tag: "v3.0.0"})

	if status, _ := w.statusBinding.Get(); status != "Encre v3.0.0 is available" {
		t.Fatalf("status bar = %q", status)
	}
	if !p.updateButton.Visible() || p.updateButton.Disabled() {
		t.Fatal("the tab should open with the Update button ready")
	}
}

func TestAnnounceDoesNotInterruptRunningUpdate(t *testing.T) {
	p := newTestPanel(t)
	p.state.found(&updater.Release{Tag: "v2.0.0"})
	p.state.updating(updater.Progress{Stage: updater.Installing})

	p.announce(&updater.Release{Tag: "v3.0.0"})

	if p.state.release.Tag != "v2.0.0" || !p.state.busy() {
		t.Fatalf("a running update should keep its release, got %+v", p.state)
	}
}

// A development build has no updater, and the tab says so instead of offering buttons that would fail.
func TestUpdateControlsWithoutUpdaterShowHint(t *testing.T) {
	test.NewApp()
	w := &MainWindow{}

	block := w.createUpdateControls().(*fyne.Container)
	var texts []string
	for _, object := range block.Objects {
		if _, isButton := object.(*widget.Button); isButton {
			t.Fatal("a build without an updater should offer no update buttons")
		}
		if label, ok := object.(*widget.Label); ok {
			texts = append(texts, label.Text)
		}
	}
	if strings.Join(texts, "|") != "Updates|Updates are checked in release builds." {
		t.Fatalf("labels = %q", texts)
	}
}

func TestTrayGainsAnUpdateItemOnce(t *testing.T) {
	test.NewApp()
	w := &MainWindow{config: config.Default()}
	w.initBindings()
	w.updates = newUpdatePanel(nil)
	w.tray = fyne.NewMenu("Encre", fyne.NewMenuItem("Settings", nil), fyne.NewMenuItem("Quit", nil))

	rel := &updater.Release{Tag: "v1.4.0"}
	w.SetAvailableUpdate(rel)
	w.SetAvailableUpdate(rel)

	if len(w.tray.Items) != 4 {
		t.Fatalf("tray has %d items, want the update entry and a separator added once", len(w.tray.Items))
	}
	if w.tray.Items[0].Label != "Update to v1.4.0…" || !w.tray.Items[1].IsSeparator {
		t.Fatalf("unexpected tray head: %q, separator=%v", w.tray.Items[0].Label, w.tray.Items[1].IsSeparator)
	}

	// A newer release while the user has not updated relabels the entry instead of adding one.
	w.SetAvailableUpdate(&updater.Release{Tag: "v1.5.0"})
	if len(w.tray.Items) != 4 || w.tray.Items[0].Label != "Update to v1.5.0…" {
		t.Fatalf("tray after a newer tag: %d items, head %q", len(w.tray.Items), w.tray.Items[0].Label)
	}
}

func TestRunUpdateIgnoresARepeatedClick(t *testing.T) {
	test.NewApp()
	p := newUpdatePanel(nil)
	p.state.found(&updater.Release{Tag: "v1.4.0"})
	p.state.updating(updater.Progress{Stage: updater.Downloading})

	p.runUpdate()

	if p.state.phase != updateRunning {
		t.Fatalf("a second runUpdate must not restart an update in flight")
	}
}
