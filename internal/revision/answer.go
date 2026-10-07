package revision

import (
	"cmp"
	"context"
	"errors"
	"strings"
)

// answerView shows a question and returns what fills in its answer; closing the view calls stop.
type answerView interface {
	Open(question string, stop func()) (update func(text string, done bool), fail func(reason string))
}

type askFunc func(ctx context.Context, question string, onText func(string)) (string, error)

// streamAnswer writes the reply into view as it arrives. The view opens at once, or at the first words
// after firstWords when that is given. Closing the view cancels the request, which is not an error, and
// an error the view showed is not returned: only one with no view to show it in.
func streamAnswer(ask askFunc, view answerView, question string, firstWords func()) error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var update func(string, bool)
	var fail func(string)
	open := func() { update, fail = view.Open(question, stop) }
	if firstWords == nil {
		open()
	}

	var written strings.Builder
	reply, err := ask(ctx, question, func(text string) {
		if update == nil {
			firstWords()
			open()
		}
		written.WriteString(text)
		update(written.String(), false)
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

// AnswerTyped answers a question typed into view, which shows it at once.
func (p *Processor) AnswerTyped(view answerView, question string) {
	_ = streamAnswer(p.Ask, view, question, nil)
}
