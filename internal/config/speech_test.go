package config

import (
	"encoding/json"
	"testing"
)

func TestSpeechCleanupDefaultsOff(t *testing.T) {
	if defaultSpeech().CleanUp {
		t.Fatal("transcript cleanup should be opt-in")
	}
}

func TestAppearanceStartsVisibleByDefault(t *testing.T) {
	if defaultAppearance().StartMinimized {
		t.Fatal("the application should start visible by default")
	}
}

func TestLoweringOtherAudioIsOnUntilSwitchedOff(t *testing.T) {
	var fresh SpeechConfig
	if err := json.Unmarshal([]byte(`{"engine":"local"}`), &fresh); err != nil {
		t.Fatal(err)
	}
	if !fresh.LowersAudio() {
		t.Fatal("a config without the setting does not lower other audio")
	}

	off := false
	fresh.LowerAudio = &off
	saved, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded SpeechConfig
	if err := json.Unmarshal(saved, &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.LowersAudio() {
		t.Fatalf("switching it off did not survive a save: %s", saved)
	}
}
