package ui

import (
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	_ "unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Fyne keeps the renderers, and with them the text a rich text draws, to itself.
//
//go:linkname cachedRenderer fyne.io/fyne/v2/internal/cache.CachedRenderer
func cachedRenderer(fyne.Widget) (fyne.WidgetRenderer, bool)

// selectableText selects across everything a rich text draws, as Fyne selects only within a plain label.
type selectableText struct {
	widget.BaseWidget
	text     *widget.RichText
	scroll   *container.Scroll
	copyText func(selected string)
	escape   func()
	typed    func(rune)

	anchor, caret int
	doubleTapped  time.Time
	runs          []textRun
	marks         *fyne.Container
	pointer       *textPointer
	hovered       *widget.Hyperlink
}

// textRun is one piece of drawn text, at a position within the selectable text; start counts the runes before it.
type textRun struct {
	text  *canvas.Text
	owner *widget.RichText
	runes []rune
	pos   fyne.Position
	size  fyne.Size
	clip  [2]fyne.Position
	line  int
	start int
	link  *widget.Hyperlink
	// linkAt is where the link the run is part of sits.
	linkAt fyne.Position
}

func newSelectableText(text *widget.RichText, copyText func(string), escape func(), typed func(rune)) *selectableText {
	s := &selectableText{text: text, copyText: copyText, escape: escape, typed: typed, marks: container.NewWithoutLayout()}
	s.pointer = &textPointer{s: s}
	s.pointer.ExtendBaseWidget(s.pointer)
	s.ExtendBaseWidget(s)
	return s
}

func (s *selectableText) CreateRenderer() fyne.WidgetRenderer {
	return &selectableRenderer{s: s}
}

func (s *selectableText) SelectedText() string {
	low, high := s.span()
	if low == high {
		return ""
	}
	var text strings.Builder
	var last *textRun
	for i := range s.runs {
		run := &s.runs[i]
		from, to := max(low-run.start, 0), min(high-run.start, len(run.runes))
		if from >= to {
			continue
		}
		if last != nil {
			text.WriteString(between(last, run))
		}
		text.WriteString(string(run.runes[from:to]))
		last = run
	}
	return text.String()
}

// between is what the eye reads between two runs: a wrapped line rejoins with what Fyne dropped at the break.
func between(before, after *textRun) string {
	if before.line == after.line {
		gap := after.pos.X - before.pos.X - before.size.Width
		if gap > 1 && !unicode.IsSpace(before.runes[len(before.runes)-1]) && !unicode.IsSpace(after.runes[0]) {
			return "\t"
		}
		return ""
	}
	wrapped := before.owner == after.owner && before.owner.Wrapping != fyne.TextWrapOff &&
		after.pos.Y-before.pos.Y-before.size.Height < 1
	if !wrapped {
		return "\n"
	}
	whole := before.owner.String()
	for _, join := range []string{" ", "", "\n"} {
		if strings.Contains(whole, before.text.Text+join+after.text.Text) {
			return join
		}
	}
	return " "
}

func (s *selectableText) Unselect() {
	s.anchor, s.caret = 0, 0
	s.paint()
}

func (s *selectableText) SelectAll() {
	s.collect()
	s.anchor, s.caret = 0, s.length()
	s.paint()
}

func (s *selectableText) span() (int, int) {
	return min(s.anchor, s.caret), max(s.anchor, s.caret)
}

func (s *selectableText) length() int {
	if len(s.runs) == 0 {
		return 0
	}
	last := s.runs[len(s.runs)-1]
	return last.start + len(last.runes)
}

func (s *selectableText) press(event *desktop.MouseEvent) {
	if event.Button != desktop.MouseButtonPrimary {
		return
	}
	s.focus()
	s.collect()
	if !s.doubleTapped.IsZero() && time.Since(s.doubleTapped) <= fyne.CurrentApp().Driver().DoubleTapDelay() {
		s.doubleTapped = time.Time{}
		s.selectLine(event.Position)
		return
	}
	s.caret = s.offsetAt(event.Position)
	if event.Modifier&fyne.KeyModifierShift == 0 {
		s.anchor = s.caret
	}
	s.paint()
}

func (s *selectableText) drag(event *fyne.DragEvent) {
	s.collect()
	s.follow(event.Position)
	s.caret = s.offsetAt(event.Position)
	s.paint()
}

// follow scrolls the answer while a drag reaches past what shows of it.
func (s *selectableText) follow(at fyne.Position) {
	if s.scroll == nil {
		return
	}
	offset := s.scroll.Offset
	top, bottom := offset.Y, offset.Y+s.scroll.Size().Height
	switch {
	case at.Y < top:
		offset.Y = at.Y
	case at.Y > bottom:
		offset.Y += at.Y - bottom
	default:
		return
	}
	s.scroll.ScrollToOffset(offset)
}

func (s *selectableText) selectWord(event *fyne.PointEvent) {
	s.doubleTapped = time.Now()
	s.collect()
	at := s.offsetAt(event.Position)
	run := s.runAt(at)
	if run == nil {
		return
	}
	col := min(at-run.start, len(run.runes)-1)
	word := func(r rune) bool { return !unicode.IsSpace(r) }
	if !word(run.runes[col]) {
		return
	}
	from, to := col, col
	for from > 0 && word(run.runes[from-1]) {
		from--
	}
	for to < len(run.runes) && word(run.runes[to]) {
		to++
	}
	s.anchor, s.caret = run.start+from, run.start+to
	s.paint()
}

func (s *selectableText) selectLine(at fyne.Position) {
	run := s.runAt(s.offsetAt(at))
	if run == nil {
		return
	}
	s.anchor, s.caret = s.length(), 0
	for _, other := range s.runs {
		if other.line == run.line {
			s.anchor, s.caret = min(s.anchor, other.start), max(s.caret, other.start+len(other.runes))
		}
	}
	s.paint()
}

func (s *selectableText) runAt(offset int) *textRun {
	for i := range s.runs {
		if offset < s.runs[i].start+len(s.runs[i].runes) {
			return &s.runs[i]
		}
	}
	if len(s.runs) == 0 {
		return nil
	}
	return &s.runs[len(s.runs)-1]
}

func (s *selectableText) menu(event *fyne.PointEvent) {
	on := fyne.CurrentApp().Driver().CanvasForObject(s)
	if on == nil {
		return
	}
	copyItem := fyne.NewMenuItem("Copy", func() { s.copyText(s.SelectedText()) })
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", copyItem, fyne.NewMenuItem("Select all", s.SelectAll)), on, event.AbsolutePosition)
}

func (s *selectableText) TypedShortcut(shortcut fyne.Shortcut) {
	switch shortcut.(type) {
	case *fyne.ShortcutCopy:
		s.copyText(s.SelectedText())
	case *fyne.ShortcutSelectAll:
		s.SelectAll()
	}
}

func (s *selectableText) FocusGained() {}
func (s *selectableText) FocusLost()   {}

func (s *selectableText) TypedRune(r rune) {
	if s.typed != nil {
		s.typed(r)
	}
}

func (s *selectableText) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape && s.escape != nil {
		s.escape()
	}
}

// linkAt is the link whose text is under a point; a link's own box reaches over the lines around it.
func (s *selectableText) linkAt(at fyne.Position) (*widget.Hyperlink, fyne.Position) {
	s.collect()
	for _, run := range s.runs {
		if run.link != nil && at.X >= run.pos.X && at.X < run.pos.X+run.size.Width && at.Y >= run.pos.Y && at.Y < run.pos.Y+run.size.Height {
			return run.link, at.Subtract(run.linkAt)
		}
	}
	return nil, fyne.Position{}
}

func (s *selectableText) open(event *fyne.PointEvent) {
	if link, at := s.linkAt(event.Position); link != nil {
		link.Tapped(&fyne.PointEvent{Position: at, AbsolutePosition: event.AbsolutePosition})
	}
}

func (s *selectableText) hover(event *desktop.MouseEvent) {
	link, at := s.linkAt(event.Position)
	if link != s.hovered {
		s.unhover()
	}
	s.hovered = link
	if link != nil {
		moved := *event
		moved.Position = at
		link.MouseMoved(&moved)
	}
}

func (s *selectableText) unhover() {
	if s.hovered != nil {
		s.hovered.MouseOut()
		s.hovered = nil
	}
}

func (s *selectableText) focus() {
	if on := fyne.CurrentApp().Driver().CanvasForObject(s); on != nil {
		on.Focus(s)
	}
}

// offsetAt is the place between runes nearest a point, reading line by line.
func (s *selectableText) offsetAt(at fyne.Position) int {
	if len(s.runs) == 0 {
		return 0
	}
	line, nearest := 0, float32(math.MaxFloat32)
	for _, run := range s.runs {
		distance := max(run.pos.Y-at.Y, at.Y-run.pos.Y-run.size.Height, 0)
		if distance < nearest {
			line, nearest = run.line, distance
		}
	}
	var last *textRun
	for i := range s.runs {
		run := &s.runs[i]
		if run.line != line {
			continue
		}
		if at.X < run.pos.X+run.size.Width {
			return run.start + run.column(at.X-run.pos.X)
		}
		last = run
	}
	return last.start + len(last.runes)
}

func (r *textRun) column(x float32) int {
	for i := range r.runes {
		if x < r.width(i)+r.measure(string(r.runes[i]))/2 {
			return i
		}
	}
	return len(r.runes)
}

func (r *textRun) width(runes int) float32 {
	return r.measure(string(r.runes[:runes]))
}

func (r *textRun) measure(text string) float32 {
	size, _ := fyne.CurrentApp().Driver().RenderedTextSize(text, r.text.TextSize, r.text.TextStyle, r.text.FontSource)
	return size.Width
}

// collect finds the text drawn now, in reading order; a link's own rich text reads as part of the text around it.
func (s *selectableText) collect() {
	s.runs = s.runs[:0]
	unbounded := [2]fyne.Position{{X: -math.MaxFloat32, Y: -math.MaxFloat32}, {X: math.MaxFloat32, Y: math.MaxFloat32}}
	var link *widget.Hyperlink
	var linkAt fyne.Position
	var walk func(object fyne.CanvasObject, origin fyne.Position, clip [2]fyne.Position, owner *widget.RichText)
	walk = func(object fyne.CanvasObject, origin fyne.Position, clip [2]fyne.Position, owner *widget.RichText) {
		if !object.Visible() {
			return
		}
		at := origin.Add(object.Position())
		var children []fyne.CanvasObject
		switch object := object.(type) {
		case *canvas.Text:
			if object.Text != "" {
				size := fyne.NewSize(object.MinSize().Width, max(object.Size().Height, object.MinSize().Height))
				s.runs = append(s.runs, textRun{text: object, owner: owner, runes: []rune(object.Text), pos: at, size: size, clip: clip, link: link, linkAt: linkAt})
			}
			return
		case *fyne.Container:
			children = object.Objects
		case fyne.Widget:
			switch object := object.(type) {
			case *widget.Hyperlink:
				link, linkAt = object, at
				defer func() { link = nil }()
			case *widget.RichText:
				if link == nil {
					owner = object
				}
			case *container.Scroll:
				end := at.Add(object.Size())
				clip = [2]fyne.Position{
					{X: max(clip[0].X, at.X), Y: max(clip[0].Y, at.Y)},
					{X: min(clip[1].X, end.X), Y: min(clip[1].Y, end.Y)},
				}
			}
			renderer, ok := cachedRenderer(object)
			if !ok {
				return
			}
			children = renderer.Objects()
		}
		for _, child := range children {
			walk(child, at, clip, owner)
		}
	}
	walk(s.text, fyne.Position{}, unbounded, s.text)
	s.order()
}

// order groups runs into lines by where their middles sit, as inline code sits a little lower than its line.
func (s *selectableText) order() {
	slices.SortStableFunc(s.runs, func(a, b textRun) int {
		return compare(a.pos.Y, b.pos.Y)
	})
	line, bottom := -1, float32(-math.MaxFloat32)
	for i := range s.runs {
		run := &s.runs[i]
		if run.pos.Y+run.size.Height/2 > bottom {
			line++
			bottom = run.pos.Y + run.size.Height
		}
		run.line = line
		bottom = max(bottom, run.pos.Y+run.size.Height)
	}
	slices.SortStableFunc(s.runs, func(a, b textRun) int {
		if a.line != b.line {
			return a.line - b.line
		}
		return compare(a.pos.X, b.pos.X)
	})
	start := 0
	for i := range s.runs {
		s.runs[i].start = start
		start += len(s.runs[i].runes)
	}
	s.anchor, s.caret = min(s.anchor, start), min(s.caret, start)
}

func compare(a, b float32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// paint marks the selection over the text, as code draws its own background, and within any scroll it sits in.
func (s *selectableText) paint() {
	low, high := s.span()
	if low == high {
		if len(s.marks.Objects) > 0 {
			s.marks.Objects = nil
			s.marks.Refresh()
		}
		return
	}
	s.collect()
	low, high = s.span()
	th := s.Theme()
	fill := th.Color(theme.ColorNameSelection, fyne.CurrentApp().Settings().ThemeVariant())
	radius := th.Size(theme.SizeNameSelectionRadius)
	var marks []fyne.CanvasObject
	line := -1
	for i := range s.runs {
		run := &s.runs[i]
		from, to := max(low-run.start, 0), min(high-run.start, len(run.runes))
		if from >= to {
			continue
		}
		left := max(run.pos.X+run.width(from), run.clip[0].X)
		right := min(run.pos.X+run.width(to), run.clip[1].X)
		top, bottom := max(run.pos.Y, run.clip[0].Y), min(run.pos.Y+run.size.Height, run.clip[1].Y)
		if right <= left || bottom <= top {
			continue
		}
		if len(marks) > 0 && line == run.line {
			last := marks[len(marks)-1]
			if end := last.Position().X + last.Size().Width; left-end < 1 {
				top, bottom = min(top, last.Position().Y), max(bottom, last.Position().Y+last.Size().Height)
				last.Move(fyne.NewPos(last.Position().X, top))
				last.Resize(fyne.NewSize(right-last.Position().X, bottom-top))
				continue
			}
		}
		line = run.line
		mark := canvas.NewRectangle(fill)
		mark.CornerRadius = min(radius, (bottom-top)/4)
		mark.Move(fyne.NewPos(left, top))
		mark.Resize(fyne.NewSize(right-left, bottom-top))
		marks = append(marks, mark)
	}
	s.marks.Objects = marks
	s.marks.Refresh()
}

type selectableRenderer struct {
	s *selectableText
}

func (r *selectableRenderer) Layout(size fyne.Size) {
	r.s.text.Resize(size)
	r.s.pointer.Resize(size)
	r.s.paint()
}

func (r *selectableRenderer) MinSize() fyne.Size { return r.s.text.MinSize() }

func (r *selectableRenderer) Refresh() {
	r.s.text.Refresh()
	r.s.paint()
}

func (r *selectableRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.s.text, r.s.marks, r.s.pointer}
}

func (r *selectableRenderer) Destroy() {}

// textPointer lies over the text to take every press, even beside a link, and hands a link its own taps and hover.
type textPointer struct {
	widget.BaseWidget
	s *selectableText
}

func (p *textPointer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewWithoutLayout())
}

func (p *textPointer) Cursor() desktop.Cursor {
	if p.s.hovered != nil {
		return p.s.hovered.Cursor()
	}
	return desktop.TextCursor
}

func (p *textPointer) MouseDown(event *desktop.MouseEvent)    { p.s.press(event) }
func (p *textPointer) MouseUp(*desktop.MouseEvent)            {}
func (p *textPointer) Dragged(event *fyne.DragEvent)          { p.s.drag(event) }
func (p *textPointer) DragEnd()                               {}
func (p *textPointer) Tapped(event *fyne.PointEvent)          { p.s.open(event) }
func (p *textPointer) DoubleTapped(event *fyne.PointEvent)    { p.s.selectWord(event) }
func (p *textPointer) TappedSecondary(event *fyne.PointEvent) { p.s.menu(event) }
func (p *textPointer) MouseIn(event *desktop.MouseEvent)      { p.s.hover(event) }
func (p *textPointer) MouseMoved(event *desktop.MouseEvent)   { p.s.hover(event) }
func (p *textPointer) MouseOut()                              { p.s.unhover() }
