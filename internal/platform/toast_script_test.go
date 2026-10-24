package platform

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestEveryQuotePowerShellKnowsIsDoubled(t *testing.T) {
	cases := map[string]string{
		"it's":                "it''s",
		"it\u2019s":           "it\u2019\u2019s",
		"\u2018a\u201a\u201b": "\u2018\u2018a\u201a\u201a\u201b\u201b",
		"café, 日本語":           "café, 日本語",
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// Text from the clipboard or a provider error must not end the string and run as script.
func TestQuotedTextCannotLeaveItsString(t *testing.T) {
	for _, quote := range []string{"'", "\u2018", "\u2019", "\u201a", "\u201b"} {
		quoted := psQuote("x" + quote + "; Remove-Item C:\\ #")
		if !strings.Contains(quoted, quote+quote+";") {
			t.Errorf("quote %q left alone: %q", quote, quoted)
		}
	}
}

func TestTheScriptIsEncodedAsUTF16LittleEndian(t *testing.T) {
	script := "$title = 'Révision terminée ✓ 🎉'"
	raw, err := base64.StdEncoding.DecodeString(encodePowerShell(script))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("odd byte count %d", len(raw))
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(units)); got != script {
		t.Fatalf("decoded %q, want %q", got, script)
	}
}

func TestALongToastStaysWithinTheCommandLine(t *testing.T) {
	long := strings.Repeat("’", 100000)
	command := encodePowerShell(toastScriptFor("me.pngwasi.encre", long, long))
	if len(command) > 32000 {
		t.Fatalf("encoded command is %d characters, over the Windows command line", len(command))
	}
}
