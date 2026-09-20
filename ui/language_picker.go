package ui

import (
	"strings"

	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/language"
)

type LanguagePicker struct {
	*widget.Select
	code          string
	excludedCode  string
	onCodeChanged func(string)
	updating      bool
}

func NewLanguagePicker(code string) *LanguagePicker {
	picker := &LanguagePicker{code: code}
	picker.Select = widget.NewSelect(labelsFor(language.All()), nil)
	picker.SetCode(code)

	picker.OnChanged = func(label string) {
		if picker.updating {
			return
		}
		if match, ok := languageByLabel(label); ok &&
			!strings.EqualFold(match.Code, picker.excludedCode) {
			picker.code = match.Code
			if picker.onCodeChanged != nil {
				picker.onCodeChanged(match.Code)
			}
		}
	}

	return picker
}

func (p *LanguagePicker) Code() string {
	return p.code
}

func (p *LanguagePicker) SetCode(code string) {
	p.updating = true
	defer func() { p.updating = false }()

	p.code = code
	p.Selected = labelFor(language.Find(code))
	p.Refresh()
}

// Excluding the other side's code keeps a translation pair from matching.
func (p *LanguagePicker) SetExcludedCode(code string) {
	p.excludedCode = code
	if strings.EqualFold(p.code, code) {
		for _, candidate := range language.All() {
			if !strings.EqualFold(candidate.Code, code) {
				p.SetCode(candidate.Code)
				break
			}
		}
	}
	p.refreshOptions()
}

func (p *LanguagePicker) SetOnCodeChanged(callback func(string)) {
	p.onCodeChanged = callback
}

func (p *LanguagePicker) refreshOptions() {
	p.updating = true
	defer func() { p.updating = false }()

	filtered := make([]language.Language, 0, len(language.All()))
	for _, candidate := range language.All() {
		if !strings.EqualFold(candidate.Code, p.excludedCode) {
			filtered = append(filtered, candidate)
		}
	}

	p.Select.Options = labelsFor(filtered)
	p.Select.Selected = labelFor(language.Find(p.code))
	p.Select.Refresh()
}

func labelFor(l language.Language) string {
	endonym := l.Endonym()
	if endonym == "" || endonym == l.Name {
		return l.Name + " (" + l.Code + ")"
	}
	return l.Name + " — " + endonym + " (" + l.Code + ")"
}

func labelsFor(languages []language.Language) []string {
	labels := make([]string, len(languages))
	for i, l := range languages {
		labels[i] = labelFor(l)
	}
	return labels
}

func languageByLabel(label string) (language.Language, bool) {
	for _, l := range language.All() {
		if labelFor(l) == label {
			return l, true
		}
	}
	return language.Language{}, false
}
