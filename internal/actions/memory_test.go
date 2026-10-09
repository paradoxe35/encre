package actions

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/history"
	"github.com/paradoxe35/encre/internal/utils"
)

func asked(pairs ...string) []history.Entry {
	var entries []history.Entry
	for i := 0; i < len(pairs); i += 2 {
		entries = append(entries, history.Entry{Kind: history.KindAsk, Original: pairs[i], Result: pairs[i+1]})
	}
	return entries
}

func TestRecallTakesTheLatestTurnsOldestFirst(t *testing.T) {
	newestFirst := asked("q3", "a3", "q2", "a2", "q1", "a1")
	got := recall(newestFirst, 2)
	want := []ai.Turn{{Question: "q2", Answer: "a2"}, {Question: "q3", Answer: "a3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recalled %v, want %v", got, want)
	}
	if got := recall(newestFirst, 10); len(got) != 3 {
		t.Fatalf("recalled %d turns of 3 asked", len(got))
	}
}

func TestRecallLeavesOutTheOldestPastTheBudget(t *testing.T) {
	long := strings.Repeat("x", memoryBudget/2)
	got := recall(asked("recent", long, "older", long, "oldest", "a"), 3)
	if len(got) != 1 || got[0].Question != "recent" {
		t.Fatalf("recalled %d turns, want only the most recent within the budget", len(got))
	}
}

type recordingProvider struct {
	prompts *[]ai.Prompt
}

func (r recordingProvider) Stream(_ context.Context, prompt ai.Prompt, onText func(string)) (string, error) {
	*r.prompts = append(*r.prompts, prompt)
	onText("answer to " + prompt.Text)
	return "answer to " + prompt.Text, nil
}
func (r recordingProvider) Name() string  { return "OpenAI" }
func (r recordingProvider) Model() string { return "m" }

func TestAskSendsAsManyEarlierQuestionsAsItIsSetToRemember(t *testing.T) {
	for memory, want := range map[int]int{1: 0, 2: 1, 3: 2} {
		t.Setenv("HOME", t.TempDir())
		if err := os.MkdirAll(utils.AppHomeDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := mentionConfig(false)
		cfg.AIProvider.Provider = "OpenAI"
		cfg.SetOperation(config.OpAsk, config.OperationConfig{TimeoutSeconds: 5, CharacterLimit: 100, Memory: memory})

		var prompts []ai.Prompt
		p := &Processor{config: cfg, providerFactory: ai.NewProviderFactory(), history: history.NewStore()}
		p.providerFactory.Register("OpenAI", recordingProvider{&prompts})
		for _, question := range []string{"first", "second", "third"} {
			if _, err := p.Ask(context.Background(), question, func(string) {}, func(string) {}); err != nil {
				t.Fatal(err)
			}
		}

		last := prompts[len(prompts)-1]
		if len(last.History) != want {
			t.Fatalf("memory %d sent %d earlier questions, want %d", memory, len(last.History), want)
		}
		if want > 0 && last.History[want-1] != (ai.Turn{Question: "second", Answer: "answer to second"}) {
			t.Fatalf("memory %d sent %v, want the latest question last", memory, last.History)
		}
	}
}
