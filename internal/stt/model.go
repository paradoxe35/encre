package stt

import "fmt"

type Model struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`

	Repo     string `json:"repo"`
	Revision string `json:"revision"`
	Filename string `json:"filename"`
	Quant    string `json:"quant"`

	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`

	Languages      []string `json:"languages"`
	License        string   `json:"license"`
	Translate      bool     `json:"translate"`
	Streaming      bool     `json:"streaming"`
	LanguageDetect bool     `json:"language_detect"`

	// WordErrorRate is a percentage, as published: 7.53 means 7.53%.
	WordErrorRate  float64 `json:"word_error_rate"`
	RealtimeFactor float64 `json:"realtime_factor"`
	AccuracyScore  float64 `json:"accuracy_score"`

	Recommended bool `json:"recommended"`
	Rank        int  `json:"rank"`
}

// Pins the revision so the repo cannot move under the checksum.
func (m Model) DownloadURL() string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s", m.Repo, m.Revision, m.Filename)
}

func (m Model) SizeMB() float64 { return float64(m.SizeBytes) / (1 << 20) }

func (m Model) Multilingual() bool { return len(m.Languages) > 1 }

func (m Model) LanguageSummary() string {
	switch len(m.Languages) {
	case 0:
		return "unknown"
	case 1:
		return m.Languages[0]
	default:
		return fmt.Sprintf("%d languages", len(m.Languages))
	}
}

func (m Model) Speaks(code string) bool {
	for _, language := range m.Languages {
		if language == code {
			return true
		}
	}
	return false
}

// LanguageAfterSwitch is the stored choice once the model changes: detection
// takes over where it exists, otherwise a spoken choice carries across.
func (m Model) LanguageAfterSwitch(previous, fallback string) string {
	return SwitchLanguage(m.Languages, m.LanguageDetect, previous, fallback)
}

// TranscribeLanguage is the code to hand the engine. A model that cannot
// detect is never left blank: the library assumes English.
func (m Model) TranscribeLanguage(preferred, fallback string) string {
	if preferred != "" && m.Speaks(preferred) {
		return preferred
	}
	if m.LanguageDetect {
		return ""
	}
	if fallback != "" && m.Speaks(fallback) {
		return fallback
	}
	if len(m.Languages) > 0 {
		return m.Languages[0]
	}
	return ""
}
