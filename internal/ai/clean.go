package ai

import "strings"

// The reply is pasted into the user's text, so a fence or quotes wrapping all of it are stripped,
// unless the source text came wrapped the same way and the model rightly kept it.
func CleanResponse(raw, source string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	source = strings.TrimSpace(source)

	if stripped := stripCodeFence(text); stripped == text || stripCodeFence(source) == source {
		text = stripped
	}
	if !quoted(source) {
		text = stripWrappingQuotes(text)
	}

	return strings.TrimSpace(text)
}

func stripCodeFence(text string) string {
	if !strings.HasPrefix(text, "```") {
		return text
	}

	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return text
	}

	closing := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimRight(lines[i], " \t\r") == "```" {
			closing = i
			break
		}
	}

	// Only unwrap a fence that closes on the final line — otherwise it is content, not wrapping.
	if closing <= 0 || closing != len(lines)-1 {
		return text
	}

	return strings.Join(lines[1:closing], "\n")
}

var quotePairs = [][2]rune{
	{'"', '"'},
	{'\'', '\''},
	{'“', '”'},
	{'«', '»'},
}

func stripWrappingQuotes(text string) string {
	runes := []rune(text)
	if len(runes) < 2 {
		return text
	}

	for _, pair := range quotePairs {
		opener, closer := pair[0], pair[1]
		if runes[0] != opener || runes[len(runes)-1] != closer {
			continue
		}

		inner := string(runes[1 : len(runes)-1])
		// A delimiter in the middle means the quotes are part of the content.
		if opener == closer && strings.ContainsRune(inner, opener) {
			continue
		}
		if opener != closer && strings.ContainsRune(inner, closer) {
			continue
		}

		return strings.TrimSpace(inner)
	}

	return text
}

// Every pair a selection may open and close with, wider than what is stripped, since a translation
// may rightly swap „…“ or 「…」 for the target language's own quotes.
var sourceQuotePairs = append([][2]rune{
	{'„', '“'}, {'‚', '‘'}, {'»', '«'}, {'‹', '›'}, {'「', '」'}, {'『', '』'}, {'‘', '’'},
}, quotePairs...)

// quoted reads only the ends, so a quotation with quotes inside it still counts.
func quoted(source string) bool {
	runes := []rune(source)
	if len(runes) < 2 {
		return false
	}
	for _, pair := range sourceQuotePairs {
		if runes[0] == pair[0] && runes[len(runes)-1] == pair[1] {
			return true
		}
	}
	return false
}
