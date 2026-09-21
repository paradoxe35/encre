package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// The test app reports no version; this stands in for a packaged build.
type versionedApp struct {
	fyne.App
	version string
}

func (a versionedApp) Metadata() fyne.AppMetadata {
	return fyne.AppMetadata{Version: a.version}
}

func TestUpdaterOnlyInReleaseBuilds(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"", false},
		{"dev", false},
		{"1.2.3-beta", false},
		{"1.2.3", true},
	}

	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			u, err := newUpdater(versionedApp{App: test.NewApp(), version: tc.version}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := u != nil; got != tc.want {
				t.Errorf("updater built = %v, want %v", got, tc.want)
			}
		})
	}
}
