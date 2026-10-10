package actions

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/ai/tools"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/prompt"
)

type scriptedModel struct {
	turns   []ai.Reply
	prompts []ai.Prompt
}

func (m *scriptedModel) Turn(_ context.Context, prompt ai.Prompt, onText func(string)) (ai.Reply, error) {
	m.prompts = append(m.prompts, prompt)
	reply := m.turns[min(len(m.prompts), len(m.turns))-1]
	if reply.Text != "" {
		onText(reply.Text)
	}
	return reply, nil
}

func fakeTool(name string, run func(json.RawMessage) (string, error)) tools.Tool {
	return tools.Tool{
		Tool:   ai.Tool{Name: name},
		Status: func(json.RawMessage) string { return "Using " + name },
		Run:    func(_ context.Context, args json.RawMessage) (string, error) { return run(args) },
	}
}

func call(name string) ai.ToolCall {
	return ai.ToolCall{ID: "call_" + name, Name: name, Arguments: json.RawMessage(`{"place":"Paris"}`)}
}

func TestAskRunsTheToolsTheModelCallsThenGivesItsAnswer(t *testing.T) {
	model := &scriptedModel{turns: []ai.Reply{
		{Text: "Let me check.", Calls: []ai.ToolCall{call("get_weather"), call("search_wikipedia")}},
		{Text: "Sunny in Paris."},
	}}
	weather := fakeTool("get_weather", func(args json.RawMessage) (string, error) { return "Sunny, 21°C at " + string(args), nil })
	broken := fakeTool("search_wikipedia", func(json.RawMessage) (string, error) { return "", errors.New("offline") })

	var statuses []string
	var alive int
	answer, err := converse(context.Background(), model, ai.Prompt{Text: "Weather?"}, []tools.Tool{weather, broken},
		func(string) {}, func(line string) { statuses = append(statuses, line) }, func() { alive++ })
	if err != nil || answer != "Sunny in Paris." {
		t.Fatalf("answered %q, %v", answer, err)
	}
	if len(statuses) != 1 || statuses[0] != "Using get_weather\nUsing search_wikipedia" {
		t.Errorf("statuses %q, want one line per call of the round", statuses)
	}
	if len(model.prompts[0].Tools) != 2 {
		t.Errorf("offered %d tools, want 2", len(model.prompts[0].Tools))
	}
	if alive < 2 {
		t.Errorf("progress was marked %d times, want before and after the lookups", alive)
	}
	step := model.prompts[1].Steps[0]
	if step.Results[0] != `Sunny, 21°C at {"place":"Paris"}` || step.Results[1] != "The lookup failed: offline" {
		t.Errorf("sent back %q", step.Results)
	}
}

func TestOnceTheRoundsAreUsedUpTheModelMustAnswer(t *testing.T) {
	looping := ai.Reply{Calls: []ai.ToolCall{call("unknown")}}
	model := &scriptedModel{turns: append(slices.Repeat([]ai.Reply{looping}, maxLookupRounds), ai.Reply{Text: "Done."})}

	answer, err := converse(context.Background(), model, ai.Prompt{Text: "q"}, nil, func(string) {}, func(string) {}, func() {})
	if err != nil || answer != "Done." {
		t.Fatalf("answered %q, %v", answer, err)
	}
	if model.prompts[0].Steps != nil || model.prompts[maxLookupRounds-1].NoMoreCalls {
		t.Error("tool calls were forbidden before the rounds were used up")
	}
	last := model.prompts[len(model.prompts)-1]
	if !last.NoMoreCalls || len(last.Steps) != maxLookupRounds {
		t.Errorf("the last request forbids calls: %v, after %d rounds", last.NoMoreCalls, len(last.Steps))
	}
	if result := last.Steps[0].Results[0]; result != "There is no tool called unknown." {
		t.Errorf("an unknown tool answered %q", result)
	}
}

func TestAModelThatIgnoresTheLimitStops(t *testing.T) {
	model := &scriptedModel{turns: []ai.Reply{{Calls: []ai.ToolCall{call("unknown")}}}}
	_, err := converse(context.Background(), model, ai.Prompt{Text: "q"}, nil, func(string) {}, func(string) {}, func() {})
	if !errors.Is(err, errKeptLookingUp) || len(model.prompts) != 7 {
		t.Fatalf("got %v after %d requests, want 6 rounds of lookups and a last one forbidding them", err, len(model.prompts))
	}
}

func TestWhatTheModelWroteBeforeLookingUpIsNotShownAsTheAnswer(t *testing.T) {
	view := &fakeView{done: make(chan shownAnswer, 1)}
	ask := func(_ context.Context, _ string, onText, onStatus func(string)) (string, error) {
		onText("Let me check.")
		onStatus("Checking the weather in Paris")
		onText("Sunny.")
		return "Sunny.", nil
	}
	if err := streamAnswer(ask, view, "Weather?", nil); err != nil {
		t.Fatal(err)
	}
	if shown := <-view.done; shown.text != "Sunny." {
		t.Fatalf("the card shows %q", shown.text)
	}
}

func TestAskWithToolsIsToldWhenToLookThingsUp(t *testing.T) {
	model := &scriptedModel{turns: []ai.Reply{{Text: "Sunny."}}}
	weather := fakeTool("get_weather", func(json.RawMessage) (string, error) { return "", nil })

	if _, err := converse(context.Background(), model, ai.Prompt{System: "My own Ask prompt.", Text: "Weather?"}, []tools.Tool{weather},
		func(string) {}, func(string) {}, func() {}); err != nil {
		t.Fatal(err)
	}
	sent := model.prompts[0]
	if sent.ToolUse != prompt.AskTools || sent.System != "My own Ask prompt." {
		t.Fatalf("sent tool use %q with system %q", sent.ToolUse, sent.System)
	}
}

func TestAskOffersToolsOnlyWhenSwitchedOn(t *testing.T) {
	if got := askTools(config.OperationConfig{}); got != nil {
		t.Errorf("tools off still offers %d", len(got))
	}
	if got := len(askTools(config.OperationConfig{Tools: true})); got != 4 {
		t.Errorf("tools on offers %d, want 4", got)
	}
}

func TestTheContextGivesTheDateTimeAndSystem(t *testing.T) {
	now := time.Date(2026, 10, 9, 14, 5, 0, 0, time.FixedZone("WAT", 3600))
	about := askContext(now)
	for _, want := range []string{"Friday, 9 October 2026, 14:05 (WAT, UTC+01:00)", "Operating system: "} {
		if !strings.Contains(about, want) {
			t.Errorf("context lacks %q:\n%s", want, about)
		}
	}
	if runtime.GOOS == "linux" && !strings.Contains(about, "Linux") {
		t.Errorf("context names the system wrongly:\n%s", about)
	}
}
