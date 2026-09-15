package ui

import (
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/history"
)

var historyFilters = []struct {
	label string
	kind  history.Kind
}{
	{"All", ""},
	{"Revision", history.KindRevise},
	{"Translation", history.KindTranslate},
	{"Speech", history.KindSpeech},
}

// historyRow caches an entry's rendered row lines so the list update path never re-formats.
type historyRow struct {
	entry  history.Entry
	title  string
	detail string
}

func (w *MainWindow) createHistorySection() fyne.CanvasObject {
	filter := widget.NewSelect(historyFilterLabels(), nil)
	filter.SetSelected("All")

	status := widget.NewLabel("")
	status.TextStyle.Italic = true

	rows := newHistoryRows(w.historyStoreRef())

	list := widget.NewList(
		func() int { return rows.len() },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.TextStyle.Bold = true
			title.Truncation = fyne.TextTruncateEllipsis
			detail := widget.NewLabel("")
			detail.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(title, detail)
		},
		func(i widget.ListItemID, item fyne.CanvasObject) {
			row, ok := rows.at(i)
			if !ok {
				return
			}
			children := item.(*fyne.Container).Objects
			children[0].(*widget.Label).SetText(row.title)
			children[1].(*widget.Label).SetText(row.detail)
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		row, ok := rows.at(i)
		if !ok {
			return
		}
		list.UnselectAll()
		showHistoryDetail(w.Window, row.entry)
	}

	refresh := func() {
		rows.load(filter.Selected)
		list.Refresh()
		status.SetText(rows.summary())
	}
	filter.OnChanged = func(string) { refresh() }
	w.refreshHistory = refresh
	refresh()

	clear := widget.NewButtonWithIcon("Clear history", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Clear history",
			"Delete all history entries? This cannot be undone.",
			func(ok bool) {
				if ok {
					if err := w.historyStoreRef().Clear(); err != nil {
						dialog.ShowError(err, w.Window)
						return
					}
					refresh()
				}
			}, w.Window)
	})
	clear.Importance = widget.LowImportance

	header := container.NewBorder(nil, nil, status, clear, filter)
	return container.NewBorder(header, nil, nil, nil, list)
}

func historyFilterLabels() []string {
	labels := make([]string, len(historyFilters))
	for i, f := range historyFilters {
		labels[i] = f.label
	}
	return labels
}

func filterKind(label string) history.Kind {
	for _, f := range historyFilters {
		if f.label == label {
			return f.kind
		}
	}
	return ""
}

// historyRows is the list's model: loaded once per filter change.
type historyRows struct {
	store *history.Store
	mu    sync.Mutex
	rows  []historyRow
}

func newHistoryRows(store *history.Store) *historyRows {
	return &historyRows{store: store}
}

func (h *historyRows) load(filter string) {
	entries := h.store.Recent(filterKind(filter))

	rows := make([]historyRow, len(entries))
	for i, entry := range entries {
		rows[i] = historyRow{entry: entry, title: historyTitle(entry), detail: historyDetail(entry)}
	}

	h.mu.Lock()
	h.rows = rows
	h.mu.Unlock()
}

func (h *historyRows) len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.rows)
}

func (h *historyRows) at(i widget.ListItemID) (historyRow, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i < 0 || i >= len(h.rows) {
		return historyRow{}, false
	}
	return h.rows[i], true
}

func (h *historyRows) summary() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch n := len(h.rows); n {
	case 0:
		return "No history yet"
	default:
		return fmt.Sprintf("%d entries", n)
	}
}

func historyTitle(entry history.Entry) string {
	label := strings.Title(string(entry.Kind))
	return fmt.Sprintf("%s · %s", label, entry.At.Format("2 Jan 15:04"))
}

// historyDetail keeps the row to one line: the result sent to the target app. The dialog shows the full original.
func historyDetail(entry history.Entry) string {
	text := entry.Result
	if text == "" {
		text = entry.Original
	}
	return strings.ReplaceAll(text, "\n", " ")
}

func showHistoryDetail(window fyne.Window, entry history.Entry) {
	var lines []string
	if entry.FromLang != "" {
		lines = append(lines,
			fmt.Sprintf("From (%s):", entry.FromLang), entry.Original, "",
			fmt.Sprintf("To (%s):", entry.ToLang), entry.Result)
	} else {
		lines = append(lines, "Result:", entry.Result)
		if entry.Original != "" && entry.Original != entry.Result {
			lines = append(lines, "", "Original:", entry.Original)
		}
	}
	if entry.Model != "" || entry.Provider != "" {
		lines = append(lines, "", "Via: "+strings.Join(nonEmpty(entry.Provider, entry.Model), " · "))
	}
	lines = append(lines, "", entry.At.Format("2 January 2006 at 15:04"))

	showTextDialog(window, "History entry", strings.Join(lines, "\n"), fyne.NewSize(460, 380))
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
