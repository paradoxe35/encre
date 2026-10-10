package ui

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func fitsChars(n int) func(string) bool {
	return func(line string) bool { return utf8.RuneCountInString(line) <= n }
}

func TestLinesBreakBetweenWordsAndInsideWordsTooLongForALine(t *testing.T) {
	got := wrapLines("one two three\n\nabcdefghijklmn end", fitsChars(7), 0)
	want := []string{"one two", "three", "", "abcdefg", "hijklmn", "end"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapped to %q, want %q", got, want)
	}
}

func TestWrappingStopsOnceItHasMoreLinesThanWanted(t *testing.T) {
	if got := wrapLines(strings.Repeat("word ", 1000), fitsChars(10), 3); len(got) != 4 {
		t.Fatalf("wrapped %d lines, want 4: one past the three wanted", len(got))
	}
}

func TestClampingKeepsShortTextAndEndsLongTextWithAnEllipsis(t *testing.T) {
	if got := clampLines("short\ntext", fitsChars(10), 3); got != "short\ntext" {
		t.Fatalf("short text became %q", got)
	}
	got := clampLines("aaaa bbbb cccc dddd eeee", fitsChars(9), 2)
	if got != "aaaa bbbb\ncccc ddd…" {
		t.Fatalf("long text clamped to %q", got)
	}
}
