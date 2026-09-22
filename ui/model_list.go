package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/stt"
)

type ModelList struct {
	widget.BaseWidget

	store           *stt.Store
	host            stt.Machine
	window          fyne.Window
	selected        string
	onSelect        func(stt.Model)
	onDeleted       func(stt.Model)
	onActiveChanged func(string)

	mu       sync.Mutex
	filtered []stt.Model
	progress map[string]stt.Progress

	list   *widget.List
	search *widget.Entry
	filter *widget.Select
}

func NewModelList(store *stt.Store, window fyne.Window, selected string, onSelect func(stt.Model)) *ModelList {
	m := &ModelList{
		store:    store,
		host:     stt.Host(),
		window:   window,
		selected: selected,
		onSelect: onSelect,
		progress: make(map[string]stt.Progress),
	}
	m.ExtendBaseWidget(m)
	m.build()
	return m
}

func (m *ModelList) build() {
	m.list = widget.NewList(m.count, m.template, m.update)

	m.search = widget.NewEntry()
	m.search.SetPlaceHolder("Search models")

	m.filter = widget.NewSelect(
		[]string{"All", "Downloaded", "Recommended", "Multilingual", "English"}, nil)
	m.filter.SetSelected("All")

	// Attached after the initial selection so neither fires before the list they refresh exists.
	m.search.OnChanged = func(string) { m.apply() }
	m.filter.OnChanged = func(string) { m.apply() }

	m.apply()
}

func (m *ModelList) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.filtered)
}

func (m *ModelList) at(i widget.ListItemID) (stt.Model, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < 0 || i >= len(m.filtered) {
		return stt.Model{}, false
	}
	return m.filtered[i], true
}

func (m *ModelList) apply() {
	query := strings.ToLower(strings.TrimSpace(m.search.Text))
	mode := m.filter.Selected

	var out []stt.Model
	for _, model := range stt.Catalogue() {
		if !matches(model, query, mode, m.store) {
			continue
		}
		out = append(out, model)
	}
	stt.RankForMachine(out, m.host, m.store.Downloaded)

	m.mu.Lock()
	m.filtered = out
	m.mu.Unlock()
	m.list.Refresh()
}

// Reload rebuilds the rows and the active label from the current catalogue,
// for when it was replaced underneath the list.
func (m *ModelList) Reload() {
	m.apply()
	if m.onActiveChanged != nil {
		m.onActiveChanged(activeModelText(m.selected, stt.Catalogue(), m.store.Downloaded))
	}
}

func matches(model stt.Model, query, mode string, store *stt.Store) bool {
	switch mode {
	case "Downloaded":
		if !store.Downloaded(model) {
			return false
		}
	case "Recommended":
		if !model.Recommended {
			return false
		}
	case "Multilingual":
		if !model.Multilingual() {
			return false
		}
	case "English":
		if !model.Speaks("en") {
			return false
		}
	}

	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(model.Name), query) ||
		strings.Contains(strings.ToLower(model.Slug), query) ||
		model.Speaks(query)
}

type modelRow struct {
	title  *widget.Label
	meta   *widget.Label
	action *widget.Button
	remove *widget.Button
	info   *widget.Button
}

// modelRowItem is what List recycles; carrying the row avoids walking the container tree.
type modelRowItem struct {
	widget.BaseWidget
	row     *modelRow
	content fyne.CanvasObject
}

func (i *modelRowItem) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(i.content)
}

func (m *ModelList) template() fyne.CanvasObject {
	row := &modelRow{
		title:  widget.NewLabel(""),
		meta:   widget.NewLabel(""),
		action: widget.NewButton("Get", nil),
		remove: widget.NewButtonWithIcon("", theme.DeleteIcon(), nil),
		info:   widget.NewButtonWithIcon("", theme.InfoIcon(), nil),
	}
	row.title.TextStyle.Bold = true
	row.title.Truncation = fyne.TextTruncateEllipsis
	row.meta.TextStyle.Italic = true
	row.meta.Truncation = fyne.TextTruncateEllipsis
	row.remove.Importance = widget.LowImportance
	row.info.Importance = widget.LowImportance

	item := &modelRowItem{
		row: row,
		content: container.NewVBox(
			container.NewBorder(nil, nil, nil,
				container.NewHBox(row.action, row.remove, row.info), row.title),
			row.meta,
		),
	}
	item.ExtendBaseWidget(item)
	return item
}

func (m *ModelList) update(i widget.ListItemID, item fyne.CanvasObject) {
	entry, ok := item.(*modelRowItem)
	if !ok {
		return
	}
	row := entry.row

	model, ok := m.at(i)
	if !ok {
		return
	}

	row.title.SetText(model.Name)
	row.meta.SetText(summarise(model, m.host))

	m.mu.Lock()
	progress, downloading := m.progress[model.ID]
	m.mu.Unlock()

	row.remove.Hide()
	row.info.Show()
	row.action.Show()

	switch {
	case downloading && progress.Stage == stt.StageVerifying:
		row.action.SetText("Verifying")
		row.action.OnTapped = nil
		row.action.Disable()

	case downloading:
		row.action.SetText(fmt.Sprintf("Cancel (%.0f%%)", progress.Fraction()*100))
		row.action.OnTapped = func() { m.store.CancelDownload(model) }
		row.action.Enable()

	case m.store.Downloaded(model):
		row.remove.Show()
		row.remove.OnTapped = func() { m.confirmDelete(model) }
		if model.ID == m.selected {
			row.action.SetText("In use")
			row.action.OnTapped = nil
			row.action.Disable()
		} else {
			row.action.SetText("Use")
			row.action.OnTapped = func() { m.choose(model) }
			row.action.Enable()
		}

	default:
		row.action.SetText(fmt.Sprintf("Get %.0f MB", model.SizeMB()))
		row.action.OnTapped = func() { m.download(model) }
		row.action.Enable()
	}

	row.info.OnTapped = func() {
		showModelDetails(m.window, model, m.host, m.store.Downloaded(model))
	}

	row.action.Refresh()
}

func summarise(model stt.Model, host stt.Machine) string {
	return strings.Join([]string{
		model.LanguageSummary(),
		fmt.Sprintf("%.0f MB", model.SizeMB()),
		stt.SpeedLabel(model, host),
	}, " · ")
}

func (m *ModelList) choose(model stt.Model) {
	m.selected = model.ID
	if m.onActiveChanged != nil {
		m.onActiveChanged(activeModelText(m.selected, stt.Catalogue(), m.store.Downloaded))
	}
	if m.onSelect != nil {
		m.onSelect(model)
	}
	m.list.Refresh()
}

func (m *ModelList) download(model stt.Model) {
	m.mu.Lock()
	m.progress[model.ID] = stt.Progress{Model: model, Total: model.SizeBytes, Stage: stt.StageDownloading}
	m.mu.Unlock()
	m.list.Refresh()

	go func() {
		err := m.store.Download(context.Background(), model, func(p stt.Progress) {
			m.mu.Lock()
			m.progress[model.ID] = p
			m.mu.Unlock()
			fyne.Do(m.list.Refresh)
		})

		m.mu.Lock()
		delete(m.progress, model.ID)
		m.mu.Unlock()

		fyne.Do(func() {
			m.list.Refresh()
			if err != nil && !errorsIsCancelled(err) {
				dialog.ShowError(err, m.window)
				return
			}
			// Adopt the model when nothing is active, or when it was chosen before its download finished.
			if err == nil && (m.selected == "" || m.selected == model.ID) {
				m.choose(model)
			}
		})
	}()
}

func (m *ModelList) confirmDelete(model stt.Model) {
	dialog.ShowConfirm("Delete model",
		fmt.Sprintf("Remove %s? You can download it again later.", model.Name),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			if err := m.store.Delete(model); err != nil {
				dialog.ShowError(err, m.window)
				return
			}
			if m.selected == model.ID {
				m.selected = ""
				if m.onActiveChanged != nil {
					m.onActiveChanged(activeModelText(m.selected, stt.Catalogue(), m.store.Downloaded))
				}
			}
			if m.onDeleted != nil {
				m.onDeleted(model)
			}
			m.list.Refresh()
		}, m.window)
}

func (m *ModelList) SetDeleted(callback func(stt.Model)) {
	m.onDeleted = callback
}

func showModelDetails(window fyne.Window, model stt.Model, host stt.Machine, downloaded bool) {
	showTextDialog(window, model.Name, stt.ModelDetails(model, host, downloaded), fyne.NewSize(420, 360))
}

func errorsIsCancelled(err error) bool {
	return err == context.Canceled || strings.Contains(err.Error(), "context canceled")
}

func (m *ModelList) CreateRenderer() fyne.WidgetRenderer {
	header := container.NewBorder(nil, nil, nil, m.filter, m.search)
	return widget.NewSimpleRenderer(container.NewBorder(header, nil, nil, nil, m.list))
}

func (m *ModelList) SetActiveChanged(callback func(string)) {
	m.onActiveChanged = callback
}

func activeModelText(selected string, models []stt.Model, downloaded func(stt.Model) bool) string {
	for _, model := range models {
		if model.ID == selected && downloaded != nil && downloaded(model) {
			return "Active model: " + model.Name
		}
	}
	return "No active model selected"
}
