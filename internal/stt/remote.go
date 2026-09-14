package stt

// RemoteProtocol is the request shape a hosted service speaks. Most take the
// OpenAI multipart POST; Gemini has no equivalent endpoint and transcribes by
// being asked to, through generateContent.
type RemoteProtocol string

const (
	ProtocolOpenAI RemoteProtocol = "openai"
	ProtocolGemini RemoteProtocol = "gemini"
)

// RemotePreset is a hosted transcription endpoint.
type RemotePreset struct {
	ID       string
	Name     string
	BaseURL  string
	Models   []string
	KeyHint  string
	Protocol RemoteProtocol
}

var RemotePresets = []RemotePreset{
	{
		ID:      "openai",
		Name:    "OpenAI",
		BaseURL: "https://api.openai.com/v1",
		Models: []string{"gpt-transcribe", "gpt-4o-transcribe", "gpt-4o-mini-transcribe",
			"whisper-1"},
		KeyHint:  "platform.openai.com",
		Protocol: ProtocolOpenAI,
	},
	{
		ID:       "groq",
		Name:     "Groq",
		BaseURL:  "https://api.groq.com/openai/v1",
		Models:   []string{"whisper-large-v3-turbo", "whisper-large-v3"},
		KeyHint:  "console.groq.com",
		Protocol: ProtocolOpenAI,
	},
	{
		ID:       "gemini",
		Name:     "Google Gemini",
		BaseURL:  "https://generativelanguage.googleapis.com",
		Models:   []string{"gemini-3.5-transcribe", "gemini-3.8-flash", "gemini-2.5-flash"},
		KeyHint:  "aistudio.google.com",
		Protocol: ProtocolGemini,
	},
	{
		ID:       "custom",
		Name:     "Custom",
		BaseURL:  "",
		Models:   nil,
		KeyHint:  "any OpenAI-compatible endpoint",
		Protocol: ProtocolOpenAI,
	},
}

// protocolFor resolves the shape to speak. An unknown id is a config naming a
// preset this build does not have, which is OpenAI-shaped in every case so far.
func protocolFor(presetID string) RemoteProtocol {
	if preset, ok := FindPreset(presetID); ok && preset.Protocol != "" {
		return preset.Protocol
	}
	return ProtocolOpenAI
}

func FindPreset(id string) (RemotePreset, bool) {
	for _, preset := range RemotePresets {
		if preset.ID == id {
			return preset, true
		}
	}
	return RemotePreset{}, false
}

func PresetNames() []string {
	names := make([]string, len(RemotePresets))
	for i, preset := range RemotePresets {
		names[i] = preset.Name
	}
	return names
}

// IsPresetModel reports whether a model is one some preset suggests, which marks
// it as a default rather than something the user typed.
func IsPresetModel(model string) bool {
	for _, preset := range RemotePresets {
		for _, known := range preset.Models {
			if known == model {
				return true
			}
		}
	}
	return false
}

func PresetByName(name string) (RemotePreset, bool) {
	for _, preset := range RemotePresets {
		if preset.Name == name {
			return preset, true
		}
	}
	return RemotePreset{}, false
}
