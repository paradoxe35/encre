package witai

import "testing"

func TestParseResponseText(t *testing.T) {
	text, err := parseResponse([]byte(`{"text":"hello world"}`))
	if err != nil || text != "hello world" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestParseResponseLegacyText(t *testing.T) {
	text, err := parseResponse([]byte(`{"_text":"hello legacy"}`))
	if err != nil || text != "hello legacy" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestParseResponseError(t *testing.T) {
	_, err := parseResponse([]byte(`{"error":"Bad auth","code":"unauthorized"}`))
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseResponseNoText(t *testing.T) {
	text, err := parseResponse([]byte(`{"confidence":0.9}`))
	if err != nil || text != "" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestParseResponseInvalidJSON(t *testing.T) {
	if _, err := parseResponse([]byte(`not json`)); err == nil {
		t.Fatal("expected an error")
	}
}
