package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheIndicatorsAreOffUntilAskedFor(t *testing.T) {
	appearance := Default().AppearanceSettings()
	if appearance.DictationIndicator || appearance.ActionIndicator {
		t.Fatal("the indicators must start switched off")
	}

	off, _ := json.Marshal(appearance)
	if strings.Contains(string(off), "indicator") {
		t.Fatalf("an unset indicator is written out: %s", off)
	}

	on, _ := json.Marshal(AppearanceConfig{DictationIndicator: true, ActionIndicator: true})
	for _, key := range []string{`"dictation_indicator":true`, `"action_indicator":true`} {
		if !strings.Contains(string(on), key) {
			t.Fatalf("%s missing from %s", key, on)
		}
	}
}

func TestTheAnnouncedUpdateIsRemembered(t *testing.T) {
	cfg := Default()
	if cfg.AnnouncedUpdate() != "" {
		t.Fatal("a fresh config already remembers an announcement")
	}
	if out, _ := json.Marshal(cfg.Meta); strings.Contains(string(out), "announced") {
		t.Fatalf("an empty announcement is written out: %s", out)
	}

	cfg.SetAnnouncedUpdate("v1.6.0")
	if cfg.AnnouncedUpdate() != "v1.6.0" {
		t.Fatal("the announcement was not kept")
	}
	if out, _ := json.Marshal(cfg.Meta); !strings.Contains(string(out), `"announced_update":"v1.6.0"`) {
		t.Fatalf("the announcement is not written out: %s", out)
	}
}
