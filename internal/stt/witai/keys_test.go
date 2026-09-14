package witai

import (
	"reflect"
	"testing"
)

func withKeys(t *testing.T, raw string) {
	t.Helper()
	rawKeys = raw
	parseKeys = newParseKeys()
	t.Cleanup(func() {
		rawKeys = ""
		parseKeys = newParseKeys()
	})
}

func TestParseKeysValid(t *testing.T) {
	withKeys(t, `[{"key":"ABC","lang":"en"},{"key":"DEF","lang":"fr"}]`)

	if !Available() {
		t.Fatal("expected keys to be available")
	}
	if got := Languages(); !reflect.DeepEqual(got, []string{"en", "fr"}) {
		t.Fatalf("got %v", got)
	}
	if token, ok := tokenFor("fr"); !ok || token != "DEF" {
		t.Fatalf("got %q, %v", token, ok)
	}
}

func TestParseKeysInvalid(t *testing.T) {
	withKeys(t, `not json`)

	if Available() {
		t.Fatal("invalid JSON must behave as no keys")
	}
	if len(Languages()) != 0 {
		t.Fatal("expected no languages")
	}
}

func TestParseKeysEmpty(t *testing.T) {
	withKeys(t, "")

	if Available() {
		t.Fatal("expected no keys")
	}
}

func TestParseKeysDropsIncomplete(t *testing.T) {
	withKeys(t, `[{"key":"ABC","lang":"en"},{"key":"","lang":"fr"},{"key":"GHI","lang":""}]`)

	if got := Languages(); !reflect.DeepEqual(got, []string{"en"}) {
		t.Fatalf("got %v", got)
	}
}
