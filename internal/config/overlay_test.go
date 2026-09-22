package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheIndicatorIsOffUntilAskedFor(t *testing.T) {
	if Default().AppearanceSettings().Indicator {
		t.Fatal("the indicator must start switched off")
	}

	off, _ := json.Marshal(Default().AppearanceSettings())
	if strings.Contains(string(off), "indicator") {
		t.Fatalf("an unset indicator is written out: %s", off)
	}

	on, _ := json.Marshal(AppearanceConfig{Indicator: true})
	if !strings.Contains(string(on), `"indicator":true`) {
		t.Fatalf("a switched-on indicator is not written out: %s", on)
	}
}
