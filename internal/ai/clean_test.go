package ai

import "testing"

func TestCleanResponse(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain text passes through", "J'ai mangé une pomme.", "J'ai mangé une pomme."},
		{"surrounding whitespace trimmed", "  bonjour \n", "bonjour"},
		{"wrapping fence removed", "```\nbonjour\n```", "bonjour"},
		{"language tagged fence removed", "```text\nbonjour\n```", "bonjour"},
		{"fence keeps inner line breaks", "```\nune\ndeux\n```", "une\ndeux"},
		{"unclosed fence left alone", "```\nbonjour", "```\nbonjour"},
		{"wrapping double quotes removed", "\"bonjour\"", "bonjour"},
		{"wrapping curly quotes removed", "“bonjour”", "bonjour"},
		{"guillemets removed", "«bonjour»", "bonjour"},
		{"inner quotes preserved", "il a dit \"non\" hier", "il a dit \"non\" hier"},
		{"quoted phrase inside prose kept", "\"non\" est ce qu'il a dit", "\"non\" est ce qu'il a dit"},
		{"matching apostrophes are unwrapped like any other pair", "'tis a test'", "tis a test"},
		{"an apostrophe inside blocks unwrapping", "'it's a test'", "'it's a test'"},
		{"fence and quotes together", "```\n\"bonjour\"\n```", "bonjour"},
		{"empty input", "", ""},
		{"whitespace only", "   \n ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanResponse(tc.raw, ""); got != tc.want {
				t.Errorf("CleanResponse(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestAWrapperTheSelectionHadIsKept(t *testing.T) {
	cases := []struct{ raw, source, want string }{
		{`"I'll be back."`, `"Ill be back."`, `"I'll be back."`},
		{"«Bonjour.»", "«Bonjour»", "«Bonjour.»"},
		{"```go\n// Retourne la somme\nfunc add() {}\n```", "```go\n// Returns the sum\nfunc add() {}\n```", "```go\n// Retourne la somme\nfunc add() {}\n```"},
		{`"Fixed sentence."`, "fixd sentence", "Fixed sentence."},
		{"```\nfixed\n```", "fixd", "fixed"},
		{`"He said 'hi' to me."`, `"He said "hi" to me."`, `"He said 'hi' to me."`},
		{"“Hello world”", "„Hallo Welt“", "“Hello world”"},
		{"“Hello”", "「こんにちは」", "“Hello”"},
		{"“Hello”", "»Hallo«", "“Hello”"},
	}
	for _, c := range cases {
		if got := CleanResponse(c.raw, c.source); got != c.want {
			t.Errorf("CleanResponse(%q, %q) = %q, want %q", c.raw, c.source, got, c.want)
		}
	}
}
