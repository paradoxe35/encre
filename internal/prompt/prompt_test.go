package prompt

import (
	"strings"
	"testing"
)

func TestDictatePromptIsDedicatedCleanupInstruction(t *testing.T) {
	if Dictate == Revise || Dictate == Translate {
		t.Fatal("dictation cleanup must not reuse another action prompt")
	}
	if !containsAll(Dictate,
		"punctuation", "filler words", "Never translate",
		"Reply with the cleaned text in the same language as the input only") {
		t.Fatal("dictation prompt is missing cleanup instructions")
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

func TestRenderTranslateSubstitutesBothLanguages(t *testing.T) {
	got := RenderTranslate(Translate, "English", "French")

	if strings.Contains(got, PlaceholderPrimary) || strings.Contains(got, PlaceholderSecondary) {
		t.Error("placeholders survived rendering")
	}
	if !strings.Contains(got, "English") || !strings.Contains(got, "French") {
		t.Error("rendered prompt is missing a language name")
	}
}

func TestRenderTranslateAppendsWhenTemplateHasNoPlaceholders(t *testing.T) {
	got := RenderTranslate("Translate this.", "English", "French")

	if !strings.HasPrefix(got, "Translate this.") {
		t.Error("custom template was not preserved")
	}
	if !strings.Contains(got, "English") || !strings.Contains(got, "French") {
		t.Errorf("language pair was not appended: %q", got)
	}
}

func TestRenderTranslateHandlesOneSidedTemplate(t *testing.T) {
	got := RenderTranslate("Always use "+PlaceholderPrimary+".", "English", "French")

	if strings.Contains(got, PlaceholderPrimary) {
		t.Error("primary placeholder survived")
	}
	if strings.Contains(got, "Otherwise, translate it into") {
		t.Error("should substitute, not append, when a placeholder is present")
	}
}

// Pinned: changing it silently changes behaviour for everyone who never edited it.
func TestReviseIsTheShippedPrompt(t *testing.T) {
	const shipped = "You are a multilingual text enhancer: fix errors, improve clarity and quality " +
		"while preserving tone, context, and intent in the original language. " +
		"Return only the enhanced version without additional text."

	if Revise != shipped {
		t.Errorf("the default revise prompt changed.\n got: %q\nwant: %q", Revise, shipped)
	}
}

func TestBuiltInPromptsConstrainOutput(t *testing.T) {
	for name, text := range map[string]string{
		"Revise":    Revise,
		"Translate": Translate,
		"Dictate":   Dictate,
	} {
		lower := strings.ToLower(text)
		if !strings.Contains(lower, "only") {
			t.Errorf("%s prompt does not restrict the model to the result alone", name)
		}
	}
}
