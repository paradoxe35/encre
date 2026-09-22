package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheIndicatorsAreOnUntilSwitchedOff(t *testing.T) {
	indicators := Default().IndicatorSettings()
	if !indicators.Dictation || !indicators.Actions {
		t.Fatal("the indicators must start switched on")
	}

	out, _ := json.Marshal(Default().AppearanceSettings())
	if !strings.Contains(string(out), `"indicators":{"dictation":true,"actions":true}`) {
		t.Fatalf("the defaults are not written whole: %s", out)
	}
}

func TestAConfigWrittenBeforeTheIndicatorsReadsAsOn(t *testing.T) {
	cfg := &Config{}
	if err := json.Unmarshal([]byte(`{"appearance":{"theme":"dark"}}`), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()

	indicators := cfg.IndicatorSettings()
	if !indicators.Dictation || !indicators.Actions {
		t.Fatalf("an old config reads as %+v, want both on", indicators)
	}
	if cfg.AppearanceSettings().Theme != "dark" {
		t.Fatal("applying the defaults touched an unrelated setting")
	}
}

func TestSwitchedOffIndicatorsStayOff(t *testing.T) {
	cfg := &Config{}
	if err := json.Unmarshal([]byte(`{"appearance":{"indicators":{"dictation":false,"actions":false}}}`), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()

	if indicators := cfg.IndicatorSettings(); indicators.Dictation || indicators.Actions {
		t.Fatalf("switched-off indicators came back as %+v", indicators)
	}
}

func TestIndicatorSettingsNeverComeBackMissing(t *testing.T) {
	cfg := &Config{}
	if indicators := cfg.IndicatorSettings(); !indicators.Dictation || !indicators.Actions {
		t.Fatalf("a bare config reads as %+v, want the defaults", indicators)
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
