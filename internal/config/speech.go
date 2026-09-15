package config

import "github.com/paradoxe35/encre/internal/stt/witai"

type SpeechEngine string

const (
	SpeechLocal  SpeechEngine = "local"
	SpeechRemote SpeechEngine = "remote"
	SpeechWitAI  SpeechEngine = "witai"
)

// The hotkey and push-to-talk mode live on the dictate action instead.
type SpeechConfig struct {
	Engine SpeechEngine `json:"engine"`

	ModelID string `json:"model_id,omitempty"`

	// InputDevice is empty for the system default.
	InputDevice string `json:"input_device,omitempty"`

	Language string `json:"language,omitempty"`

	// RemoteProvider is a preset id; endpoint and key are stored alongside so
	// transcription does not depend on a chat provider being configured.
	RemoteProvider string `json:"remote_provider,omitempty"`
	RemoteModel    string `json:"remote_model,omitempty"`
	RemoteBaseURL  string `json:"remote_base_url,omitempty"`
	RemoteAPIKey   string `json:"remote_api_key,omitempty"`

	// Off, the first words after a pause wait for a reload.
	KeepModelLoaded bool `json:"keep_model_loaded"`

	// Routes the transcript through the selected AI provider before typing.
	CleanUp bool `json:"clean_up,omitempty"`
}

func defaultSpeech() SpeechConfig {
	return SpeechConfig{
		Engine:          SpeechLocal,
		KeepModelLoaded: false,
	}
}

func (c *Config) SpeechReady() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	switch c.Speech.Engine {
	case SpeechRemote:
		return c.Speech.RemoteBaseURL != "" && c.Speech.RemoteModel != ""
	case SpeechWitAI:
		return witai.Available() && c.Speech.Language != ""
	default:
		return c.Speech.ModelID != ""
	}
}
