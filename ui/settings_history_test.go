package ui

import "testing"

func TestEveryHistoryKindIsNamedAfterItsOperation(t *testing.T) {
	for _, filter := range historyFilters[1:] {
		if filter.label == string(filter.kind) {
			t.Errorf("history kind %q has no operation of that name to label it", filter.kind)
		}
	}
}
