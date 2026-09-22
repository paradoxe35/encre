package main

import (
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func TestAReleaseIsAnnouncedOnceUntilANewerOneAppears(t *testing.T) {
	cfg := config.Default()

	if !firstAnnouncement(cfg, "v1.6.0") {
		t.Fatal("the first release was not announced")
	}
	if firstAnnouncement(cfg, "v1.6.0") {
		t.Fatal("the same release was announced again")
	}
	if !firstAnnouncement(cfg, "v1.7.0") {
		t.Fatal("a newer release was not announced")
	}
	if cfg.AnnouncedUpdate() != "v1.7.0" {
		t.Fatalf("remembered %q, want the newest", cfg.AnnouncedUpdate())
	}
}

// What was announced before a restart stays announced after it.
func TestAnAnnouncementSurvivesARestart(t *testing.T) {
	cfg := config.Default()
	cfg.SetAnnouncedUpdate("v1.6.0")
	if firstAnnouncement(cfg, "v1.6.0") {
		t.Fatal("a release remembered from a previous run was announced again")
	}
}
