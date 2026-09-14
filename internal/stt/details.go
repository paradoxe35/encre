package stt

import (
	"fmt"
	"sort"
	"strings"
)

// LanguageName resolves a catalog language code to its English name, falling back to the code itself when unknown.
func LanguageName(code string) string {
	if name, ok := languageNames[strings.ToLower(code)]; ok {
		return name
	}
	return code
}

// languageNames covers the codes the model catalog uses; not exhaustive, unknown codes display as-is.
var languageNames = map[string]string{
	"en": "English", "fr": "French", "es": "Spanish", "de": "German",
	"it": "Italian", "pt": "Portuguese", "nl": "Dutch", "pl": "Polish",
	"ru": "Russian", "uk": "Ukrainian", "cs": "Czech", "sk": "Slovak",
	"hu": "Hungarian", "ro": "Romanian", "bg": "Bulgarian", "el": "Greek",
	"tr": "Turkish", "ar": "Arabic", "he": "Hebrew", "fa": "Persian",
	"ur": "Urdu", "hi": "Hindi", "bn": "Bengali", "ta": "Tamil",
	"te": "Telugu", "mr": "Marathi", "gu": "Gujarati", "pa": "Punjabi",
	"th": "Thai", "vi": "Vietnamese", "id": "Indonesian", "ms": "Malay",
	"tl": "Filipino", "fil": "Filipino", "zh": "Chinese", "yue": "Cantonese",
	"ja": "Japanese", "ko": "Korean",
	"sv": "Swedish", "da": "Danish", "no": "Norwegian", "nb": "Norwegian",
	"nn": "Norwegian Nynorsk", "fi": "Finnish",
	"is": "Icelandic", "ca": "Catalan", "eu": "Basque", "gl": "Galician",
	"cy": "Welsh", "ga": "Irish", "af": "Afrikaans", "sw": "Swahili",
	"am": "Amharic", "ha": "Hausa", "yo": "Yoruba", "ig": "Igbo",
	"zu": "Zulu", "rw": "Kinyarwanda", "ln": "Lingala", "mg": "Malagasy",
	"hy": "Armenian", "ka": "Georgian", "az": "Azerbaijani", "kk": "Kazakh",
	"uz": "Uzbek", "mn": "Mongolian", "ne": "Nepali", "si": "Sinhala",
	"km": "Khmer", "lo": "Lao", "my": "Burmese", "sq": "Albanian",
	"sr": "Serbian", "hr": "Croatian", "sl": "Slovenian", "mk": "Macedonian",
	"bs": "Bosnian", "et": "Estonian", "lv": "Latvian", "lt": "Lithuanian",
	"ml": "Malayalam", "kn": "Kannada", "or": "Odia", "so": "Somali",
	"om": "Oromo", "jv": "Javanese", "jw": "Javanese", "su": "Sundanese",
	"eo": "Esperanto",
	"as": "Assamese", "ba": "Bashkir", "be": "Belarusian", "bo": "Tibetan",
	"br": "Breton", "fo": "Faroese", "haw": "Hawaiian", "ht": "Haitian Creole",
	"la": "Latin", "lb": "Luxembourgish", "mi": "Maori", "mt": "Maltese",
	"oc": "Occitan", "ps": "Pashto", "sa": "Sanskrit", "sd": "Sindhi",
	"sn": "Shona", "tg": "Tajik", "tk": "Turkmen", "tt": "Tatar",
	"yi": "Yiddish",
}

// SpeedLabel describes how a model is expected to keep up on this machine.
// Thresholds match the row summaries: comfortable is "fast", 10x realtime or better is "very fast".
func SpeedLabel(model Model, host Machine) string {
	switch model.Fit(host) {
	case FitTooLarge:
		return "too large for this machine"
	case FitUnknown:
		return "speed unknown"
	case FitSlow:
		return "slow here"
	default:
		if model.EstimatedRealtime(host) >= 10 {
			return "very fast here"
		}
		return "fast here"
	}
}

// ModelDetails renders the human-facing facts about a model: languages, cost to run, and
// expected behavior on this machine. Omits the catalog description and raw benchmark numbers.
func ModelDetails(model Model, host Machine, downloaded bool) string {
	var lines []string

	if len(model.Languages) == 0 {
		lines = append(lines, "Languages: unknown")
	} else {
		names := make([]string, 0, len(model.Languages))
		for _, code := range model.Languages {
			names = append(names, LanguageName(code))
		}
		sort.Strings(names)
		lines = append(lines, fmt.Sprintf("Languages (%d): %s",
			len(names), strings.Join(names, ", ")))
	}

	lines = append(lines, fmt.Sprintf("Size: %.0f MB", model.SizeMB()))
	lines = append(lines, fmt.Sprintf("Speed: %s", SpeedLabel(model, host)))
	lines = append(lines, streamingLine(model))
	lines = append(lines, detectionLine(model))
	if model.Translate {
		lines = append(lines, "Translation: can translate speech into English")
	}
	if model.License != "" {
		lines = append(lines, "License: "+model.License)
	}
	if model.WordErrorRate > 0 {
		lines = append(lines, fmt.Sprintf("Word error rate: %.1f%%", model.WordErrorRate))
	}

	state := "Not downloaded"
	switch {
	case downloaded:
		state = "Downloaded"
	case model.SizeBytes > 0:
		state = fmt.Sprintf("Not downloaded (%.0f MB)", model.SizeMB())
	}
	lines = append(lines, "State: "+state)

	return strings.Join(lines, "\n")
}

// A model that cannot detect transcribes as whatever language it is told, so
// the picker is not optional for it.
func detectionLine(model Model) string {
	if model.LanguageDetect {
		return "Language: detected automatically, or pick one"
	}
	return "Language: must be chosen; this model cannot detect it"
}

func streamingLine(model Model) string {
	// Transcript lands the moment you stop speaking; not live captions mid-sentence.
	if model.Streaming {
		return "Streaming: supported — transcribes as you speak, so text lands the moment you stop"
	}
	return "Streaming: not supported — transcription starts when you stop speaking"
}
