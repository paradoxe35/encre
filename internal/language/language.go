package language

import (
	"sort"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Name is what prompts use: the model sees a stable label regardless of the UI locale.
type Language struct {
	Code string
	Name string
}

var all = []Language{
	{Code: "en", Name: "English"},
	{Code: "zh-Hans", Name: "Chinese (Simplified)"},
	{Code: "hi", Name: "Hindi"},
	{Code: "es", Name: "Spanish"},
	{Code: "fr", Name: "French"},
	{Code: "ar", Name: "Arabic"},
	{Code: "bn", Name: "Bengali"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "ru", Name: "Russian"},
	{Code: "ur", Name: "Urdu"},
	{Code: "id", Name: "Indonesian"},
	{Code: "de", Name: "German"},
	{Code: "it", Name: "Italian"},
	{Code: "pt-BR", Name: "Portuguese (Brazil)"},
	{Code: "ja", Name: "Japanese"},
	{Code: "pa", Name: "Punjabi"},
	{Code: "mr", Name: "Marathi"},
	{Code: "te", Name: "Telugu"},
	{Code: "tr", Name: "Turkish"},
	{Code: "ta", Name: "Tamil"},
	{Code: "vi", Name: "Vietnamese"},
	{Code: "ko", Name: "Korean"},
	{Code: "fa", Name: "Persian"},
	{Code: "sw", Name: "Swahili"},
	{Code: "ms", Name: "Malay"},
	{Code: "th", Name: "Thai"},
	{Code: "gu", Name: "Gujarati"},
	{Code: "nl", Name: "Dutch"},
	{Code: "pl", Name: "Polish"},
	{Code: "uk", Name: "Ukrainian"},
	{Code: "he", Name: "Hebrew"},
	{Code: "ro", Name: "Romanian"},
	{Code: "cs", Name: "Czech"},
	{Code: "el", Name: "Greek"},
	{Code: "hu", Name: "Hungarian"},
	{Code: "sv", Name: "Swedish"},
	{Code: "da", Name: "Danish"},
	{Code: "nb", Name: "Norwegian"},
	{Code: "fi", Name: "Finnish"},
	{Code: "fil", Name: "Filipino"},
	{Code: "tl", Name: "Filipino"},
	{Code: "jv", Name: "Javanese"},
	{Code: "su", Name: "Sundanese"},
	{Code: "ml", Name: "Malayalam"},
	{Code: "kn", Name: "Kannada"},
	{Code: "or", Name: "Odia"},
	{Code: "so", Name: "Somali"},
	{Code: "om", Name: "Oromo"},
	{Code: "mk", Name: "Macedonian"},
	{Code: "bs", Name: "Bosnian"},
	{Code: "sr", Name: "Serbian"},
	{Code: "hr", Name: "Croatian"},
	{Code: "bg", Name: "Bulgarian"},
	{Code: "sk", Name: "Slovak"},
	{Code: "sl", Name: "Slovenian"},
	{Code: "lt", Name: "Lithuanian"},
	{Code: "lv", Name: "Latvian"},
	{Code: "et", Name: "Estonian"},
	{Code: "is", Name: "Icelandic"},
	{Code: "ca", Name: "Catalan"},
	{Code: "eu", Name: "Basque"},
	{Code: "gl", Name: "Galician"},
	{Code: "ga", Name: "Irish"},
	{Code: "cy", Name: "Welsh"},
	{Code: "af", Name: "Afrikaans"},
	{Code: "am", Name: "Amharic"},
	{Code: "ha", Name: "Hausa"},
	{Code: "yo", Name: "Yoruba"},
	{Code: "ig", Name: "Igbo"},
	{Code: "zu", Name: "Zulu"},
	{Code: "rw", Name: "Kinyarwanda"},
	{Code: "ln", Name: "Lingala"},
	{Code: "mg", Name: "Malagasy"},
	{Code: "hy", Name: "Armenian"},
	{Code: "ka", Name: "Georgian"},
	{Code: "az", Name: "Azerbaijani"},
	{Code: "kk", Name: "Kazakh"},
	{Code: "uz", Name: "Uzbek"},
	{Code: "mn", Name: "Mongolian"},
	{Code: "ne", Name: "Nepali"},
	{Code: "si", Name: "Sinhala"},
	{Code: "km", Name: "Khmer"},
	{Code: "lo", Name: "Lao"},
	{Code: "my", Name: "Burmese"},
	{Code: "sq", Name: "Albanian"},
	{Code: "eo", Name: "Esperanto"},
	{Code: "la", Name: "Latin"},
	{Code: "zh-Hant", Name: "Chinese (Traditional)"},
}

var byCode = func() map[string]Language {
	index := make(map[string]Language, len(all))
	for _, l := range all {
		index[strings.ToLower(l.Code)] = l
	}
	return index
}()

func All() []Language { return all }

func Find(code string) Language {
	if l, ok := byCode[strings.ToLower(strings.TrimSpace(code))]; ok {
		return l
	}
	trimmed := strings.TrimSpace(code)
	return Language{Code: trimmed, Name: trimmed}
}

func IsKnown(code string) bool {
	_, ok := byCode[strings.ToLower(strings.TrimSpace(code))]
	return ok
}

func (l Language) Endonym() string {
	tag, err := language.Parse(l.Code)
	if err != nil {
		return l.Name
	}
	if name := display.Self.Name(tag); name != "" {
		return name
	}
	return l.Name
}

func Search(query string) []Language {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all
	}

	var prefix, contains []Language
	for _, l := range all {
		code, name, endonym := strings.ToLower(l.Code), strings.ToLower(l.Name), strings.ToLower(l.Endonym())
		switch {
		case strings.HasPrefix(code, q), strings.HasPrefix(name, q), strings.HasPrefix(endonym, q):
			prefix = append(prefix, l)
		case strings.Contains(name, q), strings.Contains(endonym, q):
			contains = append(contains, l)
		}
	}
	return append(prefix, contains...)
}

// All keeps the curated popularity order; Sorted is for alphabetical pickers.
func Sorted() []Language {
	out := append([]Language(nil), all...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// The secondary is whichever of English or French the primary is not, so the pair is never degenerate.
func Defaults(localeTag string) (primary, secondary string) {
	primary = "en"
	if IsKnown(localeTag) {
		primary = Find(localeTag).Code
	} else if base, _, found := strings.Cut(localeTag, "-"); found && IsKnown(base) {
		primary = Find(base).Code
	}

	if strings.EqualFold(primary, "en") {
		return primary, "fr"
	}
	return primary, "en"
}
