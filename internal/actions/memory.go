package actions

import (
	"slices"
	"unicode/utf8"

	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/history"
)

// memoryBudget keeps earlier turns from outgrowing a small model's context.
const memoryBudget = 24000

func (p *Processor) remembered(cfg *config.Config, follows string) []ai.Turn {
	turns := cfg.Operation(config.OpAsk).Remembered()
	if turns == 0 || follows == "" {
		return nil
	}
	return recall(p.history.Recent(history.KindAsk), follows, turns)
}

// recall walks back from the exchange a question follows and returns that thread oldest first, within the budget.
func recall(asked []history.Entry, follows string, turns int) []ai.Turn {
	byID := make(map[string]history.Entry, len(asked))
	for _, entry := range asked {
		byID[entry.ID] = entry
	}

	var recalled []ai.Turn
	spent := 0
	for id := follows; len(recalled) < turns; {
		entry, ok := byID[id]
		if !ok {
			break
		}
		spent += utf8.RuneCountInString(entry.Original) + utf8.RuneCountInString(entry.Result)
		if spent > memoryBudget {
			break
		}
		recalled = append(recalled, ai.Turn{Question: entry.Original, Answer: entry.Result})
		id = entry.Follows
	}
	slices.Reverse(recalled)
	return recalled
}
