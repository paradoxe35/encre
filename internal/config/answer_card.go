package config

// AnswerCardConfig is how the answer card looks; empty values are the defaults.
type AnswerCardConfig struct {
	TextSize TextSize  `json:"text_size,omitempty"`
	Style    CardStyle `json:"style,omitempty"`
}

type CardStyle string

const (
	CardStyleSolid    CardStyle = ""
	CardStyleGlass    CardStyle = "glass"
	CardStyleGraphite CardStyle = "graphite"
	CardStyleMidnight CardStyle = "midnight"
	CardStyleAurora   CardStyle = "aurora"
	CardStylePaper    CardStyle = "paper"
	CardStyleTerminal CardStyle = "terminal"
)

var CardStyles = []CardStyle{
	CardStyleSolid, CardStyleGlass, CardStyleGraphite, CardStyleMidnight,
	CardStyleAurora, CardStylePaper, CardStyleTerminal,
}

type TextSize string

const (
	TextSizeSmall   TextSize = "small"
	TextSizeDefault TextSize = ""
	TextSizeLarge   TextSize = "large"
	TextSizeLarger  TextSize = "larger"
)

var TextSizes = []TextSize{TextSizeSmall, TextSizeDefault, TextSizeLarge, TextSizeLarger}

func (c *Config) AnswerCardSettings() AnswerCardConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AnswerCard
}

func (c *Config) SetAnswerCardSettings(card AnswerCardConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AnswerCard = card
}
