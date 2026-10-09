package actions

import (
	"slices"
	"unicode/utf8"

	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/history"
)

// memoryBudget caps the characters of earlier turns sent with a question, so a long memory cannot
// outgrow a small model's context.
const memoryBudget = 24000

func (p *Processor) remembered(cfg *config.Config) []ai.Turn {
	turns := cfg.Operation(config.OpAsk).Remembered()
	if turns == 0 {
		return nil
	}
	return recall(p.history.Recent(history.KindAsk), turns)
}

// recall takes the asked questions newest first and returns them oldest first, leaving out the oldest
// once the budget is spent.
func recall(asked []history.Entry, turns int) []ai.Turn {
	var recalled []ai.Turn
	spent := 0
	for _, entry := range asked[:min(turns, len(asked))] {
		spent += utf8.RuneCountInString(entry.Original) + utf8.RuneCountInString(entry.Result)
		if spent > memoryBudget {
			break
		}
		recalled = append(recalled, ai.Turn{Question: entry.Original, Answer: entry.Result})
	}
	slices.Reverse(recalled)
	return recalled
}
