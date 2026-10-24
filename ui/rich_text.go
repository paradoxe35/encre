package ui

import (
	"fmt"
	"hash/crc32"
	"image/color"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// adopt gives nested text the holder's theme, which Fyne gives only to top-level text.
func adopt(holder *widget.RichText, segments []widget.RichTextSegment) []widget.RichTextSegment {
	return adoptWithin(holder, holder, segments)
}

// adoptWithin takes the theme from holder, and lays lists out across within, the rich text they sit in.
func adoptWithin(holder, within *widget.RichText, segments []widget.RichTextSegment) []widget.RichTextSegment {
	adopted := make([]widget.RichTextSegment, 0, len(segments))
	for _, segment := range segments {
		switch segment := segment.(type) {
		case *widget.ListSegment:
			adopted = append(adopted, newCardList(holder, within, segment))
		case *widget.ParagraphSegment:
			segment.Texts = adoptWithin(holder, within, segment.Texts)
			adopted = append(adopted, segment)
		case *widget.TableSegment:
			adopted = append(adopted, &cardTable{TableSegment: segment, holder: holder})
		case *widget.CodeBlockSegment:
			adopted = append(adopted, &cardCode{CodeBlockSegment: segment, holder: holder})
		case *widget.SeparatorSegment:
			adopted = append(adopted, &cardRule{holder: holder})
		case *widget.CheckBoxSegment:
			text := &widget.TextSegment{Text: segment.Text, Style: widget.RichTextStyleInline}
			setHolder(text, holder)
			adopted = append(adopted, &taskBox{checked: segment.Checked, text: text, holder: holder}, text)
		case *widget.ImageSegment:
			adopted = append(adopted, imageLink(segment))
		case *widget.HyperlinkSegment:
			adopted = append(adopted, safeLink(segment))
		case *widget.TextSegment:
			for _, part := range linked(segment) {
				if text, ok := part.(*widget.TextSegment); ok {
					setHolder(text, holder)
				}
				adopted = append(adopted, part)
			}
		default:
			adopted = append(adopted, segment)
		}
	}
	return adopted
}

var bareAddress = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)

// linked makes the addresses in running text tappable, as models often cite sources bare; code keeps its text.
func linked(text *widget.TextSegment) []widget.RichTextSegment {
	if !text.Style.Inline || text.Style.TextStyle.Monospace {
		return []widget.RichTextSegment{text}
	}
	var parts []widget.RichTextSegment
	rest := text.Text
	for _, span := range bareAddress.FindAllStringIndex(text.Text, -1) {
		match := text.Text[span[0]:span[1]]
		address := strings.TrimRight(match, ".,;:!?)]}'\"")
		target, err := url.Parse(address)
		if err != nil || target.Host == "" {
			continue
		}
		before, after, _ := strings.Cut(rest, address)
		if before != "" {
			parts = append(parts, &widget.TextSegment{Text: before, Style: text.Style})
		}
		parts = append(parts, &widget.HyperlinkSegment{Text: address, URL: target})
		rest = after
	}
	if len(parts) == 0 {
		return []widget.RichTextSegment{text}
	}
	if rest != "" {
		parts = append(parts, &widget.TextSegment{Text: rest, Style: text.Style})
	}
	return parts
}

// safeLink keeps a link only to the web or mail; the answer may repeat a page that aims one at a file or another app.
func safeLink(link *widget.HyperlinkSegment) widget.RichTextSegment {
	if link.URL != nil && (link.URL.Scheme == "http" || link.URL.Scheme == "https" || link.URL.Scheme == "mailto") {
		return link
	}
	return &widget.TextSegment{Text: link.Text, Style: widget.RichTextStyleInline}
}

// imageLink stands in for an image the answer links to: fetching it would reach any address the model names, and a failed load crashes Fyne.
func imageLink(image *widget.ImageSegment) widget.RichTextSegment {
	label := "Image"
	if title := strings.TrimSpace(image.Title); title != "" {
		label += ": " + title
	}
	address, err := url.Parse(image.Source.String())
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") {
		return &widget.TextSegment{Text: label, Style: widget.RichTextStyleInline}
	}
	return &widget.HyperlinkSegment{Text: label, URL: address}
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
		case *cardList:
			for _, item := range segment.items {
				if item.marker != nil {
					do(item.marker)
				}
				eachText(item.text.Segments, do)
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

// cardList works around Fyne wrapping indented list lines at the full width.
type cardList struct {
	within *widget.RichText
	items  []listItem
}

// listItem is a marker and the rich text of its item; a list nested in an item has no marker of its own.
type listItem struct {
	marker *widget.TextSegment
	text   *widget.RichText
}

func newCardList(holder, within *widget.RichText, list *widget.ListSegment) *cardList {
	l := &cardList{within: within}
	for _, item := range list.Segments() {
		texts := item.(*widget.ParagraphSegment).Texts
		var marker *widget.TextSegment
		if bullet, ok := texts[0].(*widget.TextSegment); ok && len(texts) > 1 {
			// Fyne indents a nested list's markers with spaces; the rows here indent it already.
			bullet.Text = strings.TrimLeft(bullet.Text, " ")
			marker, texts = bullet, texts[1:]
			setHolder(marker, holder)
		}
		text := widget.NewRichText()
		text.Wrapping = fyne.TextWrapWord
		text.Segments = adoptWithin(holder, text, texts)
		l.items = append(l.items, listItem{marker: marker, text: text})
	}
	return l
}

func (l *cardList) Inline() bool { return false }

func (l *cardList) Textual() string {
	var text strings.Builder
	for _, item := range l.items {
		if item.marker != nil {
			text.WriteString(item.marker.Text)
		}
		text.WriteString(plain(item.text.Segments))
		text.WriteString("\n")
	}
	return text.String()
}

func plain(segments []widget.RichTextSegment) string {
	var text strings.Builder
	for _, segment := range segments {
		if block, ok := segment.(widget.RichTextBlock); ok {
			text.WriteString(plain(block.Segments()))
		} else {
			text.WriteString(segment.Textual())
		}
	}
	return text.String()
}

func (l *cardList) Visual() fyne.CanvasObject {
	var objects []fyne.CanvasObject
	for _, item := range l.items {
		var marker fyne.CanvasObject = layout.NewSpacer()
		if item.marker != nil {
			marker = item.marker.Visual()
		}
		objects = append(objects, marker, item.text)
	}
	return &fyne.Container{Layout: &listLayout{within: l.within}, Objects: objects}
}

func (l *cardList) Update(fyne.CanvasObject)  {}
func (l *cardList) Select(_, _ fyne.Position) {}
func (l *cardList) SelectedText() string      { return "" }
func (l *cardList) Unselect()                 {}

// listLayout lays the text's padding outside its space so it lines up with the text around the list.
type listLayout struct {
	within *widget.RichText
}

// width is the line of the rich text the list sits in: Fyne asks a block's height before laying it out.
func (l *listLayout) width() float32 {
	return l.within.Size().Width - 2*theme.SizeForWidget(theme.SizeNameInnerPadding, l.within)
}

func (l *listLayout) arrange(objects []fyne.CanvasObject, width float32, place bool) float32 {
	var column float32
	for i := 0; i < len(objects); i += 2 {
		column = max(column, objects[i].MinSize().Width)
	}
	spacing := theme.SizeForWidget(theme.SizeNameLineSpacing, l.within)
	var y float32
	for i := 0; i < len(objects); i += 2 {
		marker, text := objects[i], objects[i+1]
		pad := theme.SizeForWidget(theme.SizeNameInnerPadding, text.(*widget.RichText))
		text.Resize(fyne.NewSize(width-column+2*pad, text.Size().Height))
		height := text.MinSize().Height - 2*pad
		if place {
			marker.Move(fyne.NewPos(0, y))
			marker.Resize(fyne.NewSize(column, marker.MinSize().Height))
			text.Move(fyne.NewPos(column-pad, y-pad))
			text.Resize(fyne.NewSize(width-column+2*pad, height+2*pad))
		}
		y += height
		if i+2 < len(objects) {
			y += spacing
		}
	}
	return y
}

func (l *listLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := l.width()
	return fyne.NewSize(width, l.arrange(objects, width, false))
}

func (l *listLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.arrange(objects, size.Width, true)
}

// cardCode exists because Fyne's code block colours its panel and text from the app theme, unreadable on a card of the other variant.
type cardCode struct {
	*widget.CodeBlockSegment
	holder *widget.RichText
}

func (c *cardCode) Visual() fyne.CanvasObject {
	th := theme.CurrentForWidget(c.holder)
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	panel := canvas.NewRectangle(th.Color(theme.ColorNameInputBackground, variant))
	panel.StrokeColor = th.Color(theme.ColorNameInputBorder, variant)
	panel.StrokeWidth = 1
	panel.CornerRadius = th.Size(theme.SizeNameInputRadius)
	text := &widget.TextSegment{Text: c.Text, Style: widget.RichTextStyle{
		ColorName: theme.ColorNameForeground, SizeName: theme.SizeNameText, TextStyle: fyne.TextStyle{Monospace: true},
	}}
	setHolder(text, c.holder)
	code := container.NewStack(panel, container.NewHScroll(widget.NewRichText(text)))
	// Fyne indents quoted code only for its own segment type.
	if depth := quotingLevel(c.CodeBlockSegment); depth > 0 {
		inset := th.Size(theme.SizeNameLineSpacing) * 4 * float32(depth)
		return container.New(layout.NewCustomPaddedLayout(0, 0, inset, 0), code)
	}
	return code
}

func (c *cardCode) Update(fyne.CanvasObject) {}

func quotingLevel(block *widget.CodeBlockSegment) int {
	field := reflect.ValueOf(block).Elem().FieldByName("quotingLevel")
	if !field.IsValid() || !field.CanInt() {
		return 0
	}
	return int(field.Int())
}

// taskBox replaces Fyne's check, which takes the app theme and can be ticked off in an answer.
type taskBox struct {
	checked bool
	text    *widget.TextSegment
	holder  *widget.RichText
}

func (b *taskBox) Inline() bool { return true }

func (b *taskBox) Textual() string {
	if b.checked {
		return "[x] "
	}
	return "[ ] "
}

func (b *taskBox) Visual() fyne.CanvasObject {
	th := theme.CurrentForWidget(b.holder)
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	size := th.Size(theme.SizeNameText)
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect x="1.5" y="1.5" width="13" height="13" rx="3" fill="none" stroke="%s" stroke-width="1.5"/></svg>`,
		hexOf(th.Color(theme.ColorNamePlaceHolder, variant)))
	if b.checked {
		accent := th.Color(theme.ColorNamePrimary, variant)
		ink := "#ffffff"
		if luminance(color.NRGBAModel.Convert(accent).(color.NRGBA)) > 0.18 {
			ink = "#1c1c1e"
		}
		svg = fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect x="1" y="1" width="14" height="14" rx="3" fill="%s"/><path d="M4.5 8.2l2.4 2.4 4.6-4.9" fill="none" stroke="%s" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
			hexOf(accent), ink)
	}
	// The name keys Fyne's image cache, so each colouring needs its own.
	box := canvas.NewImageFromResource(fyne.NewStaticResource(fmt.Sprintf("task-%x.svg", crc32.ChecksumIEEE([]byte(svg))), []byte(svg)))
	box.FillMode = canvas.ImageFillContain
	// As tall as the text beside it, as Fyne drops a shorter visual by the line's baseline.
	line := fyne.MeasureText("M", size, b.text.Style.TextStyle).Height
	return &fyne.Container{Layout: &taskLayout{side: size * 0.9, gap: size * 0.4, height: line}, Objects: []fyne.CanvasObject{box}}
}

func (b *taskBox) Update(fyne.CanvasObject)  {}
func (b *taskBox) Select(_, _ fyne.Position) {}
func (b *taskBox) SelectedText() string      { return "" }
func (b *taskBox) Unselect()                 {}

type taskLayout struct{ side, gap, height float32 }

func (l *taskLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(l.side+l.gap, l.height)
}

func (l *taskLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.NewPos(0, (size.Height-l.side)/2))
	objects[0].Resize(fyne.NewSquareSize(l.side))
}

func hexOf(c color.Color) string {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return fmt.Sprintf("#%02x%02x%02x", n.R, n.G, n.B)
}

// luminance is the WCAG relative luminance.
func luminance(c color.NRGBA) float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// cardRule exists because Fyne's separator takes the app theme's colour, black across a light card on a dark app.
type cardRule struct {
	widget.SeparatorSegment
	holder *widget.RichText
}

func (r *cardRule) Visual() fyne.CanvasObject {
	th := theme.CurrentForWidget(r.holder)
	rule := canvas.NewRectangle(th.Color(theme.ColorNameSeparator, fyne.CurrentApp().Settings().ThemeVariant()))
	rule.SetMinSize(fyne.NewSize(0, th.Size(theme.SizeNameSeparatorThickness)))
	return rule
}

func (r *cardRule) Update(fyne.CanvasObject) {}

// cardTable exists because Fyne's table fills cells from the app theme, whose background is see-through.
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
