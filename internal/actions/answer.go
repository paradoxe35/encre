package actions

import (
	"cmp"
	"context"
	"errors"
	"strings"

	"github.com/paradoxe35/encre/internal/history"
)

// answerView calls stop when it is closed.
type answerView interface {
	// Following is the exchange on view, which a new question continues; empty for a fresh one.
	Following() string
	Open(question, id string, stop func()) (update func(text string, done bool), fail func(reason string), status func(line string))
}

type askFunc func(ctx context.Context, asked Question, onText, onStatus func(string)) (string, error)

// Returns only errors the view could not show; closing the view cancels without an error.
func streamAnswer(ask askFunc, view answerView, question string, firstWords func()) error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	asked := Question{Text: question, ID: history.NewID(), Follows: view.Following()}
	var update func(string, bool)
	var fail, status func(string)
	open := func() { update, fail, status = view.Open(question, asked.ID, stop) }
	if firstWords == nil {
		open()
	}
	opened := func() {
		if update == nil {
			firstWords()
			open()
		}
	}

	var written strings.Builder
	reply, err := ask(ctx, asked, func(text string) {
		opened()
		written.WriteString(text)
		update(written.String(), false)
	}, func(line string) {
		// The model looks something up before it answers, so what it wrote so far was not the answer.
		opened()
		written.Reset()
		update("", false)
		status(line)
	})

	switch {
	case errors.Is(err, context.Canceled):
		if update != nil {
			update(written.String(), true)
		}
		return nil
	case err != nil && fail != nil:
		update(written.String(), false)
		fail(err.Error())
		return nil
	case err != nil:
		return err
	}

	if update == nil {
		open()
	}
	update(cmp.Or(reply, written.String()), true)
	return nil
}

func (p *Processor) AnswerTyped(view answerView, question string) {
	_ = streamAnswer(p.Ask, view, question, nil)
}
