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

// thread asks each question after the one before it, and returns the entries newest first, as the store does.
func thread(pairs ...string) []history.Entry {
	var entries []history.Entry
	follows := ""
	for i := 0; i < len(pairs); i += 2 {
		entry := history.Entry{ID: pairs[i], Kind: history.KindAsk, Original: pairs[i], Result: pairs[i+1], Follows: follows}
		entries = append([]history.Entry{entry}, entries...)
		follows = entry.ID
	}
	return entries
}

func TestRecallTakesTheThreadOldestFirst(t *testing.T) {
	newestFirst := thread("q1", "a1", "q2", "a2", "q3", "a3")
	got := recall(newestFirst, "q3", 2)
	want := []ai.Turn{{Question: "q2", Answer: "a2"}, {Question: "q3", Answer: "a3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recalled %v, want %v", got, want)
	}
	if got := recall(newestFirst, "q3", 10); len(got) != 3 {
		t.Fatalf("recalled %d turns of 3 asked", len(got))
	}
}

func TestRecallStartsFromTheExchangeFollowed(t *testing.T) {
	got := recall(thread("q1", "a1", "q2", "a2", "q3", "a3"), "q2", 10)
	want := []ai.Turn{{Question: "q1", Answer: "a1"}, {Question: "q2", Answer: "a2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recalled %v, want %v", got, want)
	}
}

func TestRecallLeavesOutAnotherBranch(t *testing.T) {
	asked := thread("q1", "a1", "q2", "a2")
	branch := history.Entry{ID: "q3", Kind: history.KindAsk, Original: "q3", Result: "a3", Follows: "q1"}
	got := recall(append([]history.Entry{branch}, asked...), "q3", 10)
	want := []ai.Turn{{Question: "q1", Answer: "a1"}, {Question: "q3", Answer: "a3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recalled %v, want %v", got, want)
	}
}

func TestRecallStopsWhereOlderExchangesWereTrimmed(t *testing.T) {
	trimmed := thread("q1", "a1", "q2", "a2", "q3", "a3")[:2]
	got := recall(trimmed, "q3", 10)
	want := []ai.Turn{{Question: "q2", Answer: "a2"}, {Question: "q3", Answer: "a3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recalled %v, want %v", got, want)
	}
}

func TestRecallLeavesOutTheOldestPastTheBudget(t *testing.T) {
	long := strings.Repeat("x", memoryBudget/2)
	got := recall(thread("oldest", "a", "older", long, "recent", long), "recent", 3)
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

func askProcessor(t *testing.T, memory int) (*Processor, *[]ai.Prompt) {
	t.Helper()
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
	return p, &prompts
}

func ask(t *testing.T, p *Processor, question, follows string) {
	t.Helper()
	if _, err := p.Ask(context.Background(), Question{Text: question, ID: question, Follows: follows}, func(string) {}, func(string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestAskSendsAsManyEarlierQuestionsAsItIsSetToRemember(t *testing.T) {
	for memory, want := range map[int]int{1: 0, 2: 1, 3: 2} {
		p, prompts := askProcessor(t, memory)
		ask(t, p, "first", "")
		ask(t, p, "second", "first")
		ask(t, p, "third", "second")

		last := (*prompts)[len(*prompts)-1]
		if len(last.History) != want {
			t.Fatalf("memory %d sent %d earlier questions, want %d", memory, len(last.History), want)
		}
		if want > 0 && last.History[want-1] != (ai.Turn{Question: "second", Answer: "answer to second"}) {
			t.Fatalf("memory %d sent %v, want the latest question last", memory, last.History)
		}
	}
}

func TestAFreshQuestionRemembersNothing(t *testing.T) {
	p, prompts := askProcessor(t, 5)
	ask(t, p, "first", "")
	ask(t, p, "unrelated", "")

	if sent := (*prompts)[1].History; len(sent) != 0 {
		t.Fatalf("a fresh question sent %v", sent)
	}
}

func TestAQuestionAskedFromAnOlderAnswerLeavesTheLaterOnesOut(t *testing.T) {
	p, prompts := askProcessor(t, 5)
	ask(t, p, "first", "")
	ask(t, p, "second", "first")
	ask(t, p, "instead", "first")
	ask(t, p, "then", "instead")

	want := []ai.Turn{{Question: "first", Answer: "answer to first"}, {Question: "instead", Answer: "answer to instead"}}
	if sent := (*prompts)[3].History; !reflect.DeepEqual(sent, want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
	if kept := p.history.Recent(history.KindAsk); len(kept) != 4 || kept[0].Follows != "instead" {
		t.Fatalf("history kept %d questions, the newest following %q", len(kept), kept[0].Follows)
	}
}

func TestAQuestionOverTheLimitSaysSoAndWhereToRaiseIt(t *testing.T) {
	p, _ := askProcessor(t, 1)
	_, err := p.Ask(context.Background(), Question{Text: strings.Repeat("a", 101), ID: "q"}, func(string) {}, func(string) {})
	if err == nil || err.Error() != "your question is 101 characters, over the 100 limit - raise it in Settings > Actions > Ask" {
		t.Fatalf("got %v", err)
	}
}
