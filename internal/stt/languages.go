package stt

import "strings"

// NamedLanguage is a language a picker offers: Code is sent to the service in
// whatever form that service takes, Name is what appears on screen.
type NamedLanguage struct {
	Code string
	Name string
}

// whisperLanguages is openai/whisper's own list, in ISO 639-1. Groq serves the
// same weights, so it answers to exactly this.
var whisperLanguages = []NamedLanguage{
	{Code: "af", Name: "Afrikaans"},
	{Code: "sq", Name: "Albanian"},
	{Code: "am", Name: "Amharic"},
	{Code: "ar", Name: "Arabic"},
	{Code: "hy", Name: "Armenian"},
	{Code: "as", Name: "Assamese"},
	{Code: "az", Name: "Azerbaijani"},
	{Code: "ba", Name: "Bashkir"},
	{Code: "eu", Name: "Basque"},
	{Code: "be", Name: "Belarusian"},
	{Code: "bn", Name: "Bengali"},
	{Code: "bs", Name: "Bosnian"},
	{Code: "br", Name: "Breton"},
	{Code: "bg", Name: "Bulgarian"},
	{Code: "my", Name: "Burmese"},
	{Code: "yue", Name: "Cantonese"},
	{Code: "ca", Name: "Catalan"},
	{Code: "zh", Name: "Chinese"},
	{Code: "hr", Name: "Croatian"},
	{Code: "cs", Name: "Czech"},
	{Code: "da", Name: "Danish"},
	{Code: "nl", Name: "Dutch"},
	{Code: "en", Name: "English"},
	{Code: "et", Name: "Estonian"},
	{Code: "fo", Name: "Faroese"},
	{Code: "tl", Name: "Filipino"},
	{Code: "fi", Name: "Finnish"},
	{Code: "fr", Name: "French"},
	{Code: "gl", Name: "Galician"},
	{Code: "ka", Name: "Georgian"},
	{Code: "de", Name: "German"},
	{Code: "el", Name: "Greek"},
	{Code: "gu", Name: "Gujarati"},
	{Code: "ht", Name: "Haitian Creole"},
	{Code: "ha", Name: "Hausa"},
	{Code: "haw", Name: "Hawaiian"},
	{Code: "he", Name: "Hebrew"},
	{Code: "hi", Name: "Hindi"},
	{Code: "hu", Name: "Hungarian"},
	{Code: "is", Name: "Icelandic"},
	{Code: "id", Name: "Indonesian"},
	{Code: "it", Name: "Italian"},
	{Code: "ja", Name: "Japanese"},
	{Code: "jw", Name: "Javanese"},
	{Code: "kn", Name: "Kannada"},
	{Code: "kk", Name: "Kazakh"},
	{Code: "km", Name: "Khmer"},
	{Code: "ko", Name: "Korean"},
	{Code: "lo", Name: "Lao"},
	{Code: "la", Name: "Latin"},
	{Code: "lv", Name: "Latvian"},
	{Code: "ln", Name: "Lingala"},
	{Code: "lt", Name: "Lithuanian"},
	{Code: "lb", Name: "Luxembourgish"},
	{Code: "mk", Name: "Macedonian"},
	{Code: "mg", Name: "Malagasy"},
	{Code: "ms", Name: "Malay"},
	{Code: "ml", Name: "Malayalam"},
	{Code: "mt", Name: "Maltese"},
	{Code: "mi", Name: "Maori"},
	{Code: "mr", Name: "Marathi"},
	{Code: "mn", Name: "Mongolian"},
	{Code: "ne", Name: "Nepali"},
	{Code: "no", Name: "Norwegian"},
	{Code: "nn", Name: "Norwegian Nynorsk"},
	{Code: "oc", Name: "Occitan"},
	{Code: "ps", Name: "Pashto"},
	{Code: "fa", Name: "Persian"},
	{Code: "pl", Name: "Polish"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "pa", Name: "Punjabi"},
	{Code: "ro", Name: "Romanian"},
	{Code: "ru", Name: "Russian"},
	{Code: "sa", Name: "Sanskrit"},
	{Code: "sr", Name: "Serbian"},
	{Code: "sn", Name: "Shona"},
	{Code: "sd", Name: "Sindhi"},
	{Code: "si", Name: "Sinhala"},
	{Code: "sk", Name: "Slovak"},
	{Code: "sl", Name: "Slovenian"},
	{Code: "so", Name: "Somali"},
	{Code: "es", Name: "Spanish"},
	{Code: "su", Name: "Sundanese"},
	{Code: "sw", Name: "Swahili"},
	{Code: "sv", Name: "Swedish"},
	{Code: "tg", Name: "Tajik"},
	{Code: "ta", Name: "Tamil"},
	{Code: "tt", Name: "Tatar"},
	{Code: "te", Name: "Telugu"},
	{Code: "th", Name: "Thai"},
	{Code: "bo", Name: "Tibetan"},
	{Code: "tr", Name: "Turkish"},
	{Code: "tk", Name: "Turkmen"},
	{Code: "uk", Name: "Ukrainian"},
	{Code: "ur", Name: "Urdu"},
	{Code: "uz", Name: "Uzbek"},
	{Code: "vi", Name: "Vietnamese"},
	{Code: "cy", Name: "Welsh"},
	{Code: "yi", Name: "Yiddish"},
	{Code: "yo", Name: "Yoruba"},
}

// gptTranscribeLanguages adds what OpenAI documents on top of 639-1 for its
// gpt- transcribe models: selected 639-3 codes and regional zh locales.
var gptTranscribeLanguages = []NamedLanguage{
	{Code: "af", Name: "Afrikaans"},
	{Code: "sq", Name: "Albanian"},
	{Code: "am", Name: "Amharic"},
	{Code: "ar", Name: "Arabic"},
	{Code: "hy", Name: "Armenian"},
	{Code: "as", Name: "Assamese"},
	{Code: "az", Name: "Azerbaijani"},
	{Code: "ba", Name: "Bashkir"},
	{Code: "eu", Name: "Basque"},
	{Code: "be", Name: "Belarusian"},
	{Code: "bn", Name: "Bengali"},
	{Code: "bs", Name: "Bosnian"},
	{Code: "br", Name: "Breton"},
	{Code: "bg", Name: "Bulgarian"},
	{Code: "my", Name: "Burmese"},
	{Code: "yue", Name: "Cantonese"},
	{Code: "ca", Name: "Catalan"},
	{Code: "zh", Name: "Chinese"},
	{Code: "zh-hk", Name: "Chinese (Hong Kong)"},
	{Code: "zh-cn", Name: "Chinese (Mainland)"},
	{Code: "zh-tw", Name: "Chinese (Taiwan)"},
	{Code: "hr", Name: "Croatian"},
	{Code: "cs", Name: "Czech"},
	{Code: "da", Name: "Danish"},
	{Code: "nl", Name: "Dutch"},
	{Code: "en", Name: "English"},
	{Code: "et", Name: "Estonian"},
	{Code: "fo", Name: "Faroese"},
	{Code: "tl", Name: "Filipino"},
	{Code: "fi", Name: "Finnish"},
	{Code: "fr", Name: "French"},
	{Code: "gl", Name: "Galician"},
	{Code: "ka", Name: "Georgian"},
	{Code: "de", Name: "German"},
	{Code: "el", Name: "Greek"},
	{Code: "gu", Name: "Gujarati"},
	{Code: "ht", Name: "Haitian Creole"},
	{Code: "ha", Name: "Hausa"},
	{Code: "haw", Name: "Hawaiian"},
	{Code: "he", Name: "Hebrew"},
	{Code: "hi", Name: "Hindi"},
	{Code: "hu", Name: "Hungarian"},
	{Code: "is", Name: "Icelandic"},
	{Code: "id", Name: "Indonesian"},
	{Code: "it", Name: "Italian"},
	{Code: "ja", Name: "Japanese"},
	{Code: "jw", Name: "Javanese"},
	{Code: "kn", Name: "Kannada"},
	{Code: "kk", Name: "Kazakh"},
	{Code: "km", Name: "Khmer"},
	{Code: "ko", Name: "Korean"},
	{Code: "lo", Name: "Lao"},
	{Code: "la", Name: "Latin"},
	{Code: "lv", Name: "Latvian"},
	{Code: "ln", Name: "Lingala"},
	{Code: "lt", Name: "Lithuanian"},
	{Code: "lb", Name: "Luxembourgish"},
	{Code: "mk", Name: "Macedonian"},
	{Code: "mg", Name: "Malagasy"},
	{Code: "ms", Name: "Malay"},
	{Code: "ml", Name: "Malayalam"},
	{Code: "mt", Name: "Maltese"},
	{Code: "cmn", Name: "Mandarin Chinese"},
	{Code: "mi", Name: "Maori"},
	{Code: "mr", Name: "Marathi"},
	{Code: "mn", Name: "Mongolian"},
	{Code: "ne", Name: "Nepali"},
	{Code: "no", Name: "Norwegian"},
	{Code: "nn", Name: "Norwegian Nynorsk"},
	{Code: "oc", Name: "Occitan"},
	{Code: "ps", Name: "Pashto"},
	{Code: "fa", Name: "Persian"},
	{Code: "pl", Name: "Polish"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "pa", Name: "Punjabi"},
	{Code: "ro", Name: "Romanian"},
	{Code: "ru", Name: "Russian"},
	{Code: "sa", Name: "Sanskrit"},
	{Code: "sr", Name: "Serbian"},
	{Code: "sn", Name: "Shona"},
	{Code: "sd", Name: "Sindhi"},
	{Code: "si", Name: "Sinhala"},
	{Code: "sk", Name: "Slovak"},
	{Code: "sl", Name: "Slovenian"},
	{Code: "so", Name: "Somali"},
	{Code: "es", Name: "Spanish"},
	{Code: "su", Name: "Sundanese"},
	{Code: "sw", Name: "Swahili"},
	{Code: "sv", Name: "Swedish"},
	{Code: "tg", Name: "Tajik"},
	{Code: "ta", Name: "Tamil"},
	{Code: "tt", Name: "Tatar"},
	{Code: "te", Name: "Telugu"},
	{Code: "th", Name: "Thai"},
	{Code: "bo", Name: "Tibetan"},
	{Code: "tr", Name: "Turkish"},
	{Code: "tk", Name: "Turkmen"},
	{Code: "uk", Name: "Ukrainian"},
	{Code: "ur", Name: "Urdu"},
	{Code: "uz", Name: "Uzbek"},
	{Code: "vi", Name: "Vietnamese"},
	{Code: "cy", Name: "Welsh"},
	{Code: "yi", Name: "Yiddish"},
	{Code: "yo", Name: "Yoruba"},
}

// geminiTranscribeLanguages is BCP-47, the form gemini-3.5-transcribe takes in
// transcription_config.language_codes. The region is part of the choice here
// rather than something to strip: pt-BR and pt-PT are separate offers.
var geminiTranscribeLanguages = []NamedLanguage{
	{Code: "af-ZA", Name: "Afrikaans"},
	{Code: "am-ET", Name: "Amharic"},
	{Code: "ar-EG", Name: "Arabic (Egypt)"},
	{Code: "hy-AM", Name: "Armenian"},
	{Code: "as-IN", Name: "Assamese"},
	{Code: "az-AZ", Name: "Azerbaijani"},
	{Code: "be-BY", Name: "Belarusian"},
	{Code: "bn-BD", Name: "Bengali (Bangladesh)"},
	{Code: "bn-IN", Name: "Bengali (India)"},
	{Code: "bs-BA", Name: "Bosnian"},
	{Code: "bg-BG", Name: "Bulgarian"},
	{Code: "rup-BG", Name: "Bulgarian (Aromanian)"},
	{Code: "my-MM", Name: "Burmese"},
	{Code: "yue-Hant-HK", Name: "Cantonese (Traditional)"},
	{Code: "ca-ES", Name: "Catalan"},
	{Code: "ceb", Name: "Cebuano"},
	{Code: "km-KH", Name: "Central Khmer"},
	{Code: "hr-HR", Name: "Croatian"},
	{Code: "cs-CZ", Name: "Czech"},
	{Code: "da-DK", Name: "Danish"},
	{Code: "nl-NL", Name: "Dutch"},
	{Code: "en-GB", Name: "English (Great Britain)"},
	{Code: "en-IN", Name: "English (India)"},
	{Code: "en-US", Name: "English (United States)"},
	{Code: "et-EE", Name: "Estonian"},
	{Code: "fa-IR", Name: "Farsi"},
	{Code: "fil-PH", Name: "Filipino"},
	{Code: "fi-FI", Name: "Finnish"},
	{Code: "fr-FR", Name: "French"},
	{Code: "gl-ES", Name: "Galician"},
	{Code: "ka-GE", Name: "Georgian"},
	{Code: "de-DE", Name: "German"},
	{Code: "el-GR", Name: "Greek"},
	{Code: "gu-IN", Name: "Gujarati"},
	{Code: "ha-NG", Name: "Hausa"},
	{Code: "he-IL", Name: "Hebrew"},
	{Code: "hi-IN", Name: "Hindi"},
	{Code: "hu-HU", Name: "Hungarian"},
	{Code: "is-IS", Name: "Icelandic"},
	{Code: "id-ID", Name: "Indonesian"},
	{Code: "it-IT", Name: "Italian"},
	{Code: "ja-JP", Name: "Japanese"},
	{Code: "jv-ID", Name: "Javanese"},
	{Code: "kea-CV", Name: "Kabuverdianu"},
	{Code: "kn-IN", Name: "Kannada"},
	{Code: "kk-KZ", Name: "Kazakh"},
	{Code: "ko-KR", Name: "Korean"},
	{Code: "ky-KG", Name: "Kyrgyz"},
	{Code: "lv-LV", Name: "Latvian"},
	{Code: "ln-CD", Name: "Lingala"},
	{Code: "lt-LT", Name: "Lithuanian"},
	{Code: "mk-MK", Name: "Macedonian"},
	{Code: "ms-MY", Name: "Malay"},
	{Code: "ml-IN", Name: "Malayalam"},
	{Code: "mt-MT", Name: "Maltese"},
	{Code: "cmn-Hans-CN", Name: "Mandarin Chinese (Simplified)"},
	{Code: "mr-IN", Name: "Marathi"},
	{Code: "mn-MN", Name: "Mongolian"},
	{Code: "ne-NP", Name: "Nepali"},
	{Code: "nb-NO", Name: "Norwegian"},
	{Code: "or-IN", Name: "Oriya"},
	{Code: "pl-PL", Name: "Polish"},
	{Code: "pt-BR", Name: "Portuguese (Brazil)"},
	{Code: "pt-PT", Name: "Portuguese (Portugal)"},
	{Code: "pa-IN", Name: "Punjabi"},
	{Code: "pa-Guru-IN", Name: "Punjabi (Gurmukhi script)"},
	{Code: "ro-RO", Name: "Romanian"},
	{Code: "ru-RU", Name: "Russian"},
	{Code: "sr-RS", Name: "Serbian"},
	{Code: "sd-Arab-IN", Name: "Sindhi (Arabic script)"},
	{Code: "sk-SK", Name: "Slovak"},
	{Code: "sl-SI", Name: "Slovenian"},
	{Code: "es-419", Name: "Spanish (Latin America)"},
	{Code: "es-US", Name: "Spanish (United States)"},
	{Code: "sw-KE", Name: "Swahili (Kenya)"},
	{Code: "sv-SE", Name: "Swedish"},
	{Code: "tg-TJ", Name: "Tajik"},
	{Code: "te-IN", Name: "Telugu"},
	{Code: "th-TH", Name: "Thai"},
	{Code: "tr-TR", Name: "Turkish"},
	{Code: "uk-UA", Name: "Ukrainian"},
	{Code: "uz-UZ", Name: "Uzbek"},
	{Code: "vi-VN", Name: "Vietnamese"},
}

var languageSets = map[string][]NamedLanguage{
	"whisper":           whisperLanguages,
	"gpt-transcribe":    gptTranscribeLanguages,
	"gemini-transcribe": geminiTranscribeLanguages,
}

// Empty means no set is known: a custom endpoint runs whatever its owner installed, and a
// Gemini chat model is told the language in prose. Callers compare names to tell a real change
// of set from a model being typed one letter at a time, so LanguagesFor reads through this.
func LanguageSetName(presetID, model string) string {
	model = strings.ToLower(strings.TrimSpace(model))

	switch presetID {
	case "openai":
		// whisper-1 predates the gpt- models and takes only the 639-1 subset.
		if strings.HasPrefix(model, "whisper") {
			return "whisper"
		}
		return "gpt-transcribe"
	case "groq":
		return "whisper"
	case "gemini":
		if IsGeminiTranscribeModel(model) {
			return "gemini-transcribe"
		}
	}
	return ""
}

// nil when nothing is known; callers then offer free text rather than a list they cannot back.
func LanguagesFor(presetID, model string) []NamedLanguage {
	return languageSets[LanguageSetName(presetID, model)]
}

// Speech models have a language field; chat models have to be asked in prose.
func IsGeminiTranscribeModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "transcribe")
}

// A custom OpenAI-compatible server is a Whisper server often enough for its list to beat an
// empty dropdown, and the user can still type past it.
func SuggestedLanguages() []NamedLanguage {
	return whisperLanguages
}

// The language subtag is what survives a change of service: Gemini's fr-FR and Groq's fr
// are the same request.
func BaseLanguageCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if base, _, found := strings.Cut(code, "-"); found {
		return base
	}
	return code
}

func Codes(languages []NamedLanguage) []string {
	codes := make([]string, len(languages))
	for i, language := range languages {
		codes[i] = language.Code
	}
	return codes
}

// Falls back to the entry sharing the base subtag; "" when the set cannot serve it.
func MatchLanguage(languages []NamedLanguage, code string) string {
	return MatchCode(Codes(languages), code)
}

// MatchLanguage over bare codes: Gemini's fr-FR, Groq's fr and a local model's fr are one language.
func MatchCode(codes []string, want string) string {
	if want == "" {
		return ""
	}
	for _, code := range codes {
		if strings.EqualFold(code, want) {
			return code
		}
	}

	base := BaseLanguageCode(want)
	for _, code := range codes {
		if BaseLanguageCode(code) == base {
			return code
		}
	}
	return ""
}

// Settles the language when moving between engines. An engine that detects is left to; one
// that cannot is never left blank (it would silently assume English), so it keeps the previous
// choice if servable, else the system language, else English, else the first listed.
func SwitchLanguage(codes []string, detects bool, previous, system string) string {
	if detects {
		return ""
	}

	for _, candidate := range []string{previous, system, "en"} {
		if code := MatchCode(codes, candidate); code != "" {
			return code
		}
	}

	if len(codes) > 0 {
		return codes[0]
	}
	return ""
}
