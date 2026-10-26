package pcm

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Join puts back the text of the pieces Split cut, with a space only where the script uses them:
// Chinese, Japanese and Thai run on across a cut.
func Join(parts []string) string {
	var joined strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if joined.Len() > 0 {
			last, _ := utf8.DecodeLastRuneInString(joined.String())
			first, _ := utf8.DecodeRuneInString(part)
			if !unspaced(last) && !unspaced(first) {
				joined.WriteByte(' ')
			}
		}
		joined.WriteString(part)
	}
	return joined.String()
}

// unspaced is a letter of a script written without spaces, or ideographic punctuation such as 。
func unspaced(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar) ||
		0x3000 <= r && r <= 0x303f || 0xff00 <= r && r <= 0xffef
}
