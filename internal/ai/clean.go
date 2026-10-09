package ai

import "strings"

// The reply is pasted into the user's text, so a fence or quotes wrapping all of it are stripped.
func CleanResponse(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	text = stripCodeFence(text)
	text = stripWrappingQuotes(text)

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
