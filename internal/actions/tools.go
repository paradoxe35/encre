package actions

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/ai/tools"
	"github.com/paradoxe35/encre/internal/config"
)

const maxLookupRounds = 4

func askTools(ask config.OperationConfig) []tools.Tool {
	if !ask.Tools {
		return nil
	}
	return tools.Set()
}

var systemNames = map[string]string{"darwin": "macOS", "linux": "Linux", "windows": "Windows"}

// askContext is what the model cannot know: when and where the question is asked from.
func askContext(now time.Time) string {
	system := systemNames[runtime.GOOS]
	if system == "" {
		system = runtime.GOOS
	}
	return fmt.Sprintf("Context:\n- Local date and time: %s\n- Operating system: %s",
		now.Format("Monday, 2 January 2006, 15:04 (MST, UTC-07:00)"), system)
}

var errKeptLookingUp = errors.New("the model kept looking things up instead of answering - ask again, or turn off its tools")

// converse runs each round's calls together; once the rounds are used up, the model must answer.
// keepAlive marks progress, as lookups take time without the model writing anything.
func converse(ctx context.Context, model ai.ToolUser, prompt ai.Prompt, toolset []tools.Tool, onText, onStatus func(string), keepAlive func()) (string, error) {
	byName := make(map[string]tools.Tool, len(toolset))
	for _, tool := range toolset {
		prompt.Tools = append(prompt.Tools, tool.Tool)
		byName[tool.Name] = tool
	}

	for round := 1; ; round++ {
		prompt.NoMoreCalls = round > maxLookupRounds
		reply, err := model.Turn(ctx, prompt, onText)
		switch {
		case err != nil, len(reply.Calls) == 0:
			return reply.Text, err
		case prompt.NoMoreCalls && reply.Text != "":
			return reply.Text, nil
		case prompt.NoMoreCalls:
			return "", errKeptLookingUp
		}

		keepAlive()
		results := lookUp(ctx, byName, reply.Calls, onStatus)
		keepAlive()
		prompt.Steps = append(prompt.Steps, ai.Step{Calls: reply.Calls, Results: results})
	}
}

// A failed or unknown call answers with why, so the model can carry on without it.
func lookUp(ctx context.Context, byName map[string]tools.Tool, calls []ai.ToolCall, onStatus func(string)) []string {
	statuses := make([]string, 0, len(calls))
	for _, call := range calls {
		if tool, ok := byName[call.Name]; ok {
			statuses = append(statuses, tool.Status(call.Arguments))
		}
	}
	onStatus(strings.Join(statuses, "\n"))

	results := make([]string, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Go(func() {
			tool, ok := byName[call.Name]
			if !ok {
				results[i] = "There is no tool called " + call.Name + "."
				return
			}
			result, err := tool.Run(ctx, call.Arguments)
			if err != nil {
				result = "The lookup failed: " + err.Error()
			}
			results[i] = result
		})
	}
	wg.Wait()
	return results
}
