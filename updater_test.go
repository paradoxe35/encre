package main

import (
	"context"
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	updater "github.com/paradoxe35/go-updater"
)

// The test app reports no version; this stands in for a packaged build.
type versionedApp struct {
	fyne.App
	version string
	release bool
}

func (a versionedApp) Metadata() fyne.AppMetadata {
	return fyne.AppMetadata{Version: a.version, Release: a.release}
}

func TestUpdaterOnlyInReleaseBuilds(t *testing.T) {
	cases := []struct {
		name    string
		version string
		release bool
		want    bool
	}{
		{"no version", "", true, false},
		{"dev", "dev", true, false},
		{"beta", "1.2.3-beta", true, false},
		{"release", "1.2.3", true, true},
		// go run and go build read the version from FyneApp.toml.
		{"development build", "1.2.3", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := newUpdater(versionedApp{App: test.NewApp(), version: tc.version, release: tc.release}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := u != nil; got != tc.want {
				t.Errorf("updater built = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeInstall stands in for go-updater: before is its BeforeRelaunch hook.
type fakeInstall struct {
	before    func()
	installed bool
	relaunch  error
}

func (f *fakeInstall) Check(context.Context) (*updater.Release, bool, error) { return nil, false, nil }

func (f *fakeInstall) Update(context.Context, *updater.Release, ...updater.Option) error {
	if !f.installed {
		return errors.New("download failed")
	}
	f.before()
	return f.relaunch
}

func TestAFailedRelaunchGivesTheShortcutsBack(t *testing.T) {
	cases := []struct {
		name            string
		install         *fakeInstall
		wantHandedOver  bool
		wantResumed     bool
		wantUpdateError bool
	}{
		{"install fails", &fakeInstall{}, false, false, true},
		{"relaunch fails", &fakeInstall{installed: true, relaunch: errors.New("no such file")}, true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var handedOver, resumed bool
			guard := &relaunchGuard{
				handOver: func() { handedOver = true },
				resume:   func() { resumed = true },
			}
			tc.install.before = guard.beforeRelaunch
			guard.Updater = tc.install

			err := guard.Update(context.Background(), &updater.Release{})
			if (err != nil) != tc.wantUpdateError {
				t.Fatalf("error %v", err)
			}
			if handedOver != tc.wantHandedOver || resumed != tc.wantResumed {
				t.Fatalf("handed over %v, resumed %v; want %v, %v", handedOver, resumed, tc.wantHandedOver, tc.wantResumed)
			}
		})
	}
}

func TestOnlyTheRelaunchThatFailedIsResumed(t *testing.T) {
	resumes := 0
	install := &fakeInstall{installed: true, relaunch: errors.New("no such file")}
	guard := &relaunchGuard{Updater: install, handOver: func() {}, resume: func() { resumes++ }}
	install.before = guard.beforeRelaunch

	guard.Update(context.Background(), &updater.Release{})
	install.installed = false
	guard.Update(context.Background(), &updater.Release{})
	if resumes != 1 {
		t.Fatalf("resumed %d times, want once", resumes)
	}
}
