package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
)

var textSizeLabels = map[config.TextSize]string{
	config.TextSizeSmall:   "Small",
	config.TextSizeDefault: "Default",
	config.TextSizeLarge:   "Large",
	config.TextSizeLarger:  "Larger",
}

var cardStyleLabels = map[config.CardStyle]string{
	config.CardStyleGlass:    "Glass",
	config.CardStyleSolid:    "Solid",
	config.CardStyleGraphite: "Graphite",
	config.CardStyleMidnight: "Midnight",
	config.CardStyleAurora:   "Aurora",
	config.CardStylePaper:    "Paper",
	config.CardStyleTerminal: "Terminal",
}

func (w *MainWindow) answerTextSizeSelect() fyne.CanvasObject {
	w.answerTextSize = labelSelect(w, config.TextSizes, textSizeLabels, w.config.AnswerCardSettings().TextSize)
	return w.answerTextSize
}

func (w *MainWindow) answerCardStyleSelect() fyne.CanvasObject {
	w.answerCardStyle = labelSelect(w, config.CardStyles, cardStyleLabels, w.config.AnswerCardSettings().Style)
	return w.answerCardStyle
}

func (w *MainWindow) answerCardSettings() config.AnswerCardConfig {
	card := w.config.AnswerCardSettings()
	card.TextSize = selectedValue(w.answerTextSize, textSizeLabels, card.TextSize)
	card.Style = selectedValue(w.answerCardStyle, cardStyleLabels, card.Style)
	return card
}

func labelSelect[V comparable](w *MainWindow, values []V, labels map[V]string, current V) *widget.Select {
	options := make([]string, len(values))
	for i, value := range values {
		options[i] = labels[value]
	}
	sel := w.dirtySelect(options, nil)
	sel.SetSelected(labels[current])
	return sel
}

func selectedValue[V comparable](sel *widget.Select, labels map[V]string, fallback V) V {
	for value, label := range labels {
		if label == sel.Selected {
			return value
		}
	}
	return fallback
}
