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
