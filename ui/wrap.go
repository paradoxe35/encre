package ui

import (
	"sort"
	"strings"
	"unicode"
)

// wrapLines breaks text as Fyne's word wrapping draws it: between words, and inside a word wider than a line.
// It stops once it has more than most lines, which is all a caller capping the text needs.
func wrapLines(text string, fits func(string) bool, most int) []string {
	var lines []string
	full := func() bool { return most > 0 && len(lines) > most }
	for paragraph := range strings.SplitSeq(text, "\n") {
		line := ""
		for word := range strings.FieldsSeq(paragraph) {
			if joined := strings.TrimPrefix(line+" "+word, " "); fits(joined) {
				line = joined
				continue
			}
			if line != "" {
				lines = append(lines, line)
			}
			for !fits(word) && !full() {
				runes := []rune(word)
				cut := max(sort.Search(len(runes), func(i int) bool { return !fits(string(runes[:i+1])) }), 1)
				lines = append(lines, string(runes[:cut]))
				word = string(runes[cut:])
			}
			line = word
			if full() {
				return lines
			}
		}
		lines = append(lines, line)
		if full() {
			return lines
		}
	}
	return lines
}

// clampLines keeps the first most lines of text, ending the last with an ellipsis when some were left out.
func clampLines(text string, fits func(string) bool, most int) string {
	lines := wrapLines(text, fits, most)
	if len(lines) <= most {
		return text
	}
	kept := lines[:most]
	last := []rune(strings.TrimRightFunc(kept[most-1], unicode.IsSpace))
	for len(last) > 0 && !fits(string(last)+"…") {
		last = last[:len(last)-1]
	}
	kept[most-1] = strings.TrimRightFunc(string(last), unicode.IsSpace) + "…"
	return strings.Join(kept, "\n")
}
