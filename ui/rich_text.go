package ui

import (
	"image/color"
	"reflect"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// adopt gives text nested in lists and tables the theme of the rich text it shows in, which Fyne gives
// only to top-level text.
func adopt(holder *widget.RichText, segments []widget.RichTextSegment) []widget.RichTextSegment {
	adopted := make([]widget.RichTextSegment, len(segments))
	for i, segment := range segments {
		switch segment := segment.(type) {
		case *widget.ListSegment:
			adopted[i] = &widget.ParagraphSegment{Texts: adopt(holder, segment.Segments())}
		case *widget.ParagraphSegment:
			segment.Texts = adopt(holder, segment.Texts)
			adopted[i] = segment
		case *widget.TableSegment:
			adopted[i] = &cardTable{TableSegment: segment, holder: holder}
		case *widget.TextSegment:
			setHolder(segment, holder)
			adopted[i] = segment
		default:
			adopted[i] = segment
		}
	}
	return adopted
}

func eachText(segments []widget.RichTextSegment, do func(*widget.TextSegment)) {
	for _, segment := range segments {
		switch segment := segment.(type) {
		case *widget.TextSegment:
			do(segment)
		case widget.RichTextBlock:
			eachText(segment.Segments(), do)
		case *cardTable:
			for _, row := range segment.rows() {
				for _, cell := range row {
					eachText(cell, do)
				}
			}
		}
	}
}

func setHolder(text *widget.TextSegment, holder *widget.RichText) {
	field := reflect.ValueOf(text).Elem().FieldByName("parent")
	if !field.IsValid() || field.Type() != reflect.TypeOf(holder) {
		return
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(holder))
}

// cardTable draws a markdown table in the card's theme: Fyne's own fills its cells from the app's
// theme, whose background is see-through.
type cardTable struct {
	*widget.TableSegment
	holder *widget.RichText
}

func (t *cardTable) rows() [][][]widget.RichTextSegment {
	if t.Headers == nil {
		return t.Rows
	}
	return append([][][]widget.RichTextSegment{t.Headers}, t.Rows...)
}

func (t *cardTable) Visual() fyne.CanvasObject {
	th := theme.CurrentForWidget(t.holder)
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	rows := t.rows()
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return widget.NewRichText()
	}

	var cells []fyne.CanvasObject
	for r, row := range rows {
		header := r == 0 && t.Headers != nil
		for c := range cols {
			var segments []widget.RichTextSegment
			if c < len(row) {
				segments = row[c]
			}
			cells = append(cells, tableCell(th, variant, segments, header, t.align(c)))
		}
	}
	lines := make([]fyne.CanvasObject, len(rows)-1)
	for i := range lines {
		lines[i] = canvas.NewRectangle(th.Color(theme.ColorNameSeparator, variant))
	}
	grid := &fyne.Container{Layout: &tableLayout{cols: cols, rows: len(rows)}, Objects: append(cells, lines...)}
	return container.NewHScroll(grid)
}

func (t *cardTable) align(col int) fyne.TextAlign {
	if col < len(t.Alignments) {
		return t.Alignments[col]
	}
	return fyne.TextAlignLeading
}

func tableCell(th fyne.Theme, variant fyne.ThemeVariant, segments []widget.RichTextSegment, header bool, align fyne.TextAlign) fyne.CanvasObject {
	if len(segments) == 0 {
		segments = []widget.RichTextSegment{&widget.TextSegment{Text: " ", Style: widget.RichTextStyleInline}}
	}
	var texts []fyne.CanvasObject
	for _, segment := range segments {
		if link, ok := segment.(*widget.HyperlinkSegment); ok {
			texts = append(texts, widget.NewHyperlink(link.Text, link.URL))
			continue
		}
		text, ok := segment.(*widget.TextSegment)
		if !ok {
			text = &widget.TextSegment{Text: segment.Textual(), Style: widget.RichTextStyleInline}
		}
		colorName, sizeName := text.Style.ColorName, text.Style.SizeName
		if colorName == "" {
			colorName = theme.ColorNameForeground
		}
		if sizeName == "" {
			sizeName = theme.SizeNameText
		}
		label := canvas.NewText(text.Text, th.Color(colorName, variant))
		label.TextStyle = text.Style.TextStyle
		label.TextStyle.Bold = label.TextStyle.Bold || header
		label.TextSize = th.Size(sizeName)
		texts = append(texts, label)
	}

	fill := color.Color(color.Transparent)
	if header {
		fill = th.Color(theme.ColorNameInputBackground, variant)
	}
	switch align {
	case fyne.TextAlignTrailing:
		texts = append([]fyne.CanvasObject{layout.NewSpacer()}, texts...)
	case fyne.TextAlignCenter:
		texts = append(append([]fyne.CanvasObject{layout.NewSpacer()}, texts...), layout.NewSpacer())
	}
	content := &fyne.Container{Layout: layout.NewCustomPaddedHBoxLayout(0), Objects: texts}
	return container.NewStack(canvas.NewRectangle(fill), container.NewPadded(content))
}

// tableLayout puts a line under every row but the last; the lines follow the cells in the objects.
type tableLayout struct {
	cols, rows int
}

const tableLine = 1

func (l *tableLayout) measure(objects []fyne.CanvasObject) (widths, heights []float32) {
	widths, heights = make([]float32, l.cols), make([]float32, l.rows)
	for i, cell := range objects[:l.cols*l.rows] {
		size := cell.MinSize()
		widths[i%l.cols] = max(widths[i%l.cols], size.Width)
		heights[i/l.cols] = max(heights[i/l.cols], size.Height)
	}
	return widths, heights
}

func (l *tableLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	widths, heights := l.measure(objects)
	var size fyne.Size
	for _, w := range widths {
		size.Width += w
	}
	for _, h := range heights {
		size.Height += h
	}
	size.Height += float32(l.rows-1) * tableLine
	return size
}

func (l *tableLayout) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	widths, heights := l.measure(objects)
	total := l.MinSize(objects).Width
	lines := objects[l.cols*l.rows:]
	var y float32
	for r, height := range heights {
		var x float32
		for c, width := range widths {
			cell := objects[r*l.cols+c]
			cell.Move(fyne.NewPos(x, y))
			cell.Resize(fyne.NewSize(width, height))
			x += width
		}
		y += height
		if r < len(lines) {
			lines[r].Move(fyne.NewPos(0, y))
			lines[r].Resize(fyne.NewSize(total, tableLine))
			y += tableLine
		}
	}
}
