package ui

import (
	"image/color"
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
	later    func(time.Duration, func())

	anchor, caret int
	doubleTapped  time.Time
	pressed       time.Time
	pressedLink   *widget.Hyperlink
	runs          []textRun
	bars          []innerBar
	hooked        map[*container.Scroll]bool
	marks         *fyne.Container
	pointer       *textPointer
	hovered       *widget.Hyperlink
	pointerAt     fyne.Position
	// pointerFrame is where the text lay when pointerAt was last told, which no move reports again.
	pointerFrame textFrame

	dragged, dragging, following bool
	dragAt                       fyne.Position
}

// innerBar is the scrollbar of a scroll within the text, at its place within the selectable text.
type innerBar struct {
	scroll *container.Scroll
	area   scrollArea
	pos    fyne.Position
}

// scrollArea is how Fyne's own scrollbar area, which is internal, shows.
type scrollArea interface {
	fyne.Widget
	desktop.Hoverable
	fyne.Tappable
}

func (b *innerBar) contains(at fyne.Position) bool {
	return within(at, b.pos, b.area.Size())
}

// handle is the bar under a point, which Fyne drags; it hides while the area is thin and still.
func (b *innerBar) handle(at fyne.Position) fyne.Draggable {
	renderer, ok := cachedRenderer(b.area)
	if !ok {
		return nil
	}
	for _, object := range renderer.Objects() {
		if handle, ok := object.(fyne.Draggable); ok && object.Visible() && within(at, b.pos.Add(object.Position()), object.Size()) {
			return handle
		}
	}
	return nil
}

func within(at, pos fyne.Position, size fyne.Size) bool {
	return at.X >= pos.X && at.X < pos.X+size.Width && at.Y >= pos.Y && at.Y < pos.Y+size.Height
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
	s := &selectableText{text: text, copyText: copyText, escape: escape, typed: typed, marks: container.NewWithoutLayout(),
		later: func(wait time.Duration, run func()) { time.AfterFunc(wait, func() { fyne.Do(run) }) }}
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

func (s *selectableText) textUpTo(offset int) string {
	var text strings.Builder
	for _, run := range s.runs {
		if run.start >= offset {
			break
		}
		text.WriteString(string(run.runes[:min(offset-run.start, len(run.runes))]))
	}
	return text.String()
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
	delay := fyne.CurrentApp().Driver().DoubleTapDelay()
	again := time.Since(s.pressed) <= delay
	s.pressed, s.pressedLink, s.dragged, s.dragging = time.Now(), nil, false, false
	if !s.doubleTapped.IsZero() && time.Since(s.doubleTapped) <= delay {
		s.doubleTapped = time.Time{}
		s.selectLine(event.Position)
		return
	}
	shift := event.Modifier&fyne.KeyModifierShift != 0
	if !again && !shift {
		s.pressedLink, _ = s.linkAt(event.Position)
	}
	s.caret = s.offsetAt(event.Position)
	if !shift {
		s.anchor = s.caret
	}
	s.paint()
}

// release opens a link on letting go, as a tap waits out a double click first.
func (s *selectableText) release(event *desktop.MouseEvent) {
	link := s.pressedLink
	s.pressedLink = nil
	if link == nil || s.dragged || event.Button != desktop.MouseButtonPrimary {
		return
	}
	if over, at := s.linkAt(event.Position); sameLink(over, link) {
		over.Tapped(&fyne.PointEvent{Position: at, AbsolutePosition: event.AbsolutePosition})
	}
}

// A render while the button is down draws the pressed link anew, as the same text to the same address.
func sameLink(a, b *widget.Hyperlink) bool {
	if a == nil || b == nil || a == b {
		return a == b
	}
	return a.Text == b.Text && a.URL != nil && b.URL != nil && a.URL.String() == b.URL.String()
}

// drag takes the absolute position, as the driver's relative one stays where the text was when the drag began.
func (s *selectableText) drag(event *fyne.DragEvent) {
	s.dragged, s.dragging, s.dragAt = true, true, event.AbsolutePosition
	s.dragTo()
}

func (s *selectableText) dragTo() {
	s.collect()
	at := s.dragAt.Subtract(fyne.CurrentApp().Driver().AbsolutePositionForObject(s))
	if s.follow(at) && !s.following {
		s.following = true
		s.later(followEvery, s.keepFollowing)
	}
	s.caret = s.offsetAt(at)
	s.paint()
}

const followEvery = 40 * time.Millisecond

// keepFollowing scrolls on while the button is held past the answer, though the pointer stays still.
func (s *selectableText) keepFollowing() {
	s.following = false
	if s.dragging && s.Visible() && fyne.CurrentApp().Driver().CanvasForObject(s) != nil {
		s.dragTo()
	}
}

func (s *selectableText) endDrag() {
	s.dragging = false
}

// follow scrolls the answer while a drag reaches past what shows of it, and reports whether it moved.
func (s *selectableText) follow(at fyne.Position) bool {
	if s.scroll == nil {
		return false
	}
	was := s.scroll.Offset
	offset := was
	top, bottom := offset.Y, offset.Y+s.scroll.Size().Height
	switch {
	case at.Y < top:
		offset.Y = at.Y
	case at.Y > bottom:
		offset.Y += at.Y - bottom
	default:
		return false
	}
	s.scroll.ScrollToOffset(offset)
	return s.scroll.Offset != was
}

// selectWord takes in the runs beside that touch it, as a word can change style midway.
func (s *selectableText) selectWord(event *fyne.PointEvent) {
	s.doubleTapped = time.Now()
	s.collect()
	i, col := s.hit(event.Position)
	if i < 0 {
		return
	}
	kind := wordKind(s.runs[i].runes[col])
	if kind == spaceKind {
		return
	}
	first, from := i, col
	for {
		run := &s.runs[first]
		for from > 0 && wordKind(run.runes[from-1]) == kind {
			from--
		}
		if from > 0 || first == 0 || !touching(&s.runs[first-1], run) || wordKind(s.runs[first-1].runes[len(s.runs[first-1].runes)-1]) != kind {
			break
		}
		first--
		from = len(s.runs[first].runes)
	}
	last, to := i, col+1
	for {
		run := &s.runs[last]
		for to < len(run.runes) && wordKind(run.runes[to]) == kind {
			to++
		}
		if to < len(run.runes) || last+1 == len(s.runs) || !touching(run, &s.runs[last+1]) || wordKind(s.runs[last+1].runes[0]) != kind {
			break
		}
		last++
		to = 0
	}
	s.anchor, s.caret = s.runs[first].start+from, s.runs[last].start+to
	s.paint()
}

const (
	spaceKind = iota
	letterKind
	punctuationKind
)

func wordKind(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return spaceKind
	case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_':
		return letterKind
	}
	return punctuationKind
}

func touching(before, after *textRun) bool {
	return before.line == after.line && after.pos.X-before.pos.X-before.size.Width <= 1
}

func (s *selectableText) selectLine(at fyne.Position) {
	i, _ := s.hit(at)
	if i < 0 {
		return
	}
	s.anchor, s.caret = s.length(), 0
	for _, other := range s.runs {
		if other.line == s.runs[i].line {
			s.anchor, s.caret = min(s.anchor, other.start), max(s.caret, other.start+len(other.runes))
		}
	}
	s.paint()
}

// hit is the run under a point, or the nearest on its line, and the rune of it there.
func (s *selectableText) hit(at fyne.Position) (int, int) {
	if len(s.runs) == 0 {
		return -1, 0
	}
	line := s.lineAt(at.Y)
	hit, nearest := -1, float32(math.MaxFloat32)
	for i := range s.runs {
		run := &s.runs[i]
		if run.line != line {
			continue
		}
		if distance := max(run.pos.X-at.X, at.X-run.pos.X-run.size.Width, 0); distance < nearest {
			hit, nearest = i, distance
		}
	}
	return hit, s.runs[hit].runeAt(at.X - s.runs[hit].pos.X)
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
		if run.link != nil && within(at, run.pos, run.size) {
			return run.link, at.Subtract(run.linkAt)
		}
	}
	return nil, fyne.Position{}
}

func (s *selectableText) barAt(at fyne.Position) *innerBar {
	s.collect()
	for i := range s.bars {
		if s.bars[i].contains(at) {
			return &s.bars[i]
		}
	}
	return nil
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
	line := s.lineAt(at.Y)
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

func (s *selectableText) lineAt(y float32) int {
	line, nearest := 0, float32(math.MaxFloat32)
	for _, run := range s.runs {
		if distance := max(run.pos.Y-y, y-run.pos.Y-run.size.Height, 0); distance < nearest {
			line, nearest = run.line, distance
		}
	}
	return line
}

// runeAt is the rune under x, or the nearest end one.
func (r *textRun) runeAt(x float32) int {
	for i := 1; i < len(r.runes); i++ {
		if x < r.width(i) {
			return i - 1
		}
	}
	return len(r.runes) - 1
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
	s.runs, s.bars = s.runs[:0], s.bars[:0]
	hooked := make(map[*container.Scroll]bool, len(s.hooked))
	unbounded := [2]fyne.Position{{X: -math.MaxFloat32, Y: -math.MaxFloat32}, {X: math.MaxFloat32, Y: math.MaxFloat32}}
	var link *widget.Hyperlink
	var linkAt fyne.Position
	var walk func(object fyne.CanvasObject, at fyne.Position, clip [2]fyne.Position, owner *widget.RichText)
	walk = func(object fyne.CanvasObject, at fyne.Position, clip [2]fyne.Position, owner *widget.RichText) {
		if !object.Visible() {
			return
		}
		var children []fyne.CanvasObject
		switch object := object.(type) {
		case *canvas.Text:
			if object.Text != "" {
				size := fyne.NewSize(object.MinSize().Width, max(object.Size().Height, object.MinSize().Height))
				s.runs = append(s.runs, textRun{text: object, owner: owner, runes: []rune(object.Text), pos: at, size: size, clip: clip, link: link, linkAt: linkAt})
			}
			return
		case *fyne.Container:
			if task, ok := object.Layout.(*taskLayout); ok {
				// Read but never drawn, so a copied task keeps whether it is done.
				text := canvas.NewText(task.textual, color.Transparent)
				text.TextSize = theme.SizeForWidget(theme.SizeNameText, owner)
				s.runs = append(s.runs, textRun{text: text, owner: owner, runes: []rune(text.Text), pos: at, size: object.Size(), clip: clip})
				return
			}
			children = object.Objects
		case fyne.Widget:
			switch object := object.(type) {
			case *widget.Hyperlink:
				link, linkAt = object, at
				defer func() { link = nil }()
				drawn(object)
			case *widget.RichText:
				if link == nil {
					owner = object
				}
			case *container.Scroll, *sideScroll:
				// A sideScroll's renderer is kept under it, not under the scroll it wraps.
				scroll := sideways(object)
				end := at.Add(object.Size())
				clip = [2]fyne.Position{
					{X: max(clip[0].X, at.X), Y: max(clip[0].Y, at.Y)},
					{X: min(clip[1].X, end.X), Y: min(clip[1].Y, end.Y)},
				}
				s.hook(scroll, hooked)
				if renderer, ok := cachedRenderer(object); ok {
					for _, child := range renderer.Objects() {
						if area, ok := child.(scrollArea); ok && area.Visible() {
							s.bars = append(s.bars, innerBar{scroll: scroll, area: area, pos: at.Add(area.Position())})
						}
					}
				}
				// The scroll moves its content after telling of the scroll, so the offset is where it will be.
				walk(scroll.Content, at.Subtract(scroll.Offset), clip, owner)
				return
			}
			renderer, ok := cachedRenderer(object)
			if !ok {
				return
			}
			children = renderer.Objects()
		}
		for _, child := range children {
			walk(child, at.Add(child.Position()), clip, owner)
		}
	}
	walk(s.text, s.text.Position(), unbounded, s.text)
	s.hooked = hooked
	s.order()
}

// drawn builds the renderer of a link a render made anew, which Fyne would leave to the next paint.
func drawn(link *widget.Hyperlink) {
	if _, ok := cachedRenderer(link); ok {
		return
	}
	link.MinSize()
	if renderer, ok := cachedRenderer(link); ok {
		renderer.Layout(link.Size())
	}
}

// hook repaints the marks as a scroll within the text scrolls; each render brings new scrolls, so only those drawn now are kept.
func sideways(object fyne.Widget) *container.Scroll {
	if side, ok := object.(*sideScroll); ok {
		return side.Scroll
	}
	return object.(*container.Scroll)
}

func (s *selectableText) hook(scroll *container.Scroll, hooked map[*container.Scroll]bool) {
	hooked[scroll] = true
	if s.hooked[scroll] {
		return
	}
	scrolled := scroll.OnScrolled
	scroll.OnScrolled = func(offset fyne.Position) {
		if scrolled != nil {
			scrolled(offset)
		}
		s.paint()
	}
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

// Refresh keeps the selection only while the text up to it reads the same, as a streamed answer can reshape what came before.
// A link still under a resting pointer stays hovered, though each render may draw it anew.
func (r *selectableRenderer) Refresh() {
	s := r.s
	_, high := s.span()
	before := s.textUpTo(high)
	s.text.Refresh()
	if s.hovered != nil && s.frame() == s.pointerFrame {
		s.hover(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: s.pointerAt}})
	} else {
		s.unhover()
	}
	if high > 0 {
		s.collect()
		if s.textUpTo(high) != before {
			s.anchor, s.caret = 0, 0
		}
	}
	s.paint()
}

// frame moves when the text scrolls or the card grows, leaving the resting pointer over something else.
func (s *selectableText) frame() textFrame {
	return textFrame{fyne.CurrentApp().Driver().AbsolutePositionForObject(s.pointer), s.pointer.Size()}
}

type textFrame struct {
	at   fyne.Position
	size fyne.Size
}

func (r *selectableRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.s.text, r.s.marks, r.s.pointer}
}

func (r *selectableRenderer) Destroy() {}

// textPointer lies over the text to take every press, even beside a link, and hands a link its own presses and hover,
// and a scrollbar within the text its own presses, drags and hover.
type textPointer struct {
	widget.BaseWidget
	s *selectableText

	over       desktop.Hoverable
	bar        *innerBar
	handle     fyne.Draggable
	barDragged bool
}

func (p *textPointer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewWithoutLayout())
}

// Cursor reads the last place the pointer moved to, as the driver asks it before telling of the move.
func (p *textPointer) Cursor() desktop.Cursor {
	if p.s.barAt(p.s.pointerAt) != nil {
		return desktop.DefaultCursor
	}
	if link, _ := p.s.linkAt(p.s.pointerAt); link != nil {
		return link.Cursor()
	}
	return desktop.TextCursor
}

func (p *textPointer) MouseDown(event *desktop.MouseEvent) {
	p.bar, p.handle, p.barDragged = nil, nil, false
	if bar := p.s.barAt(event.Position); bar != nil && event.Button == desktop.MouseButtonPrimary {
		held := *bar
		p.bar, p.handle = &held, bar.handle(event.Position)
		return
	}
	p.s.press(event)
}

func (p *textPointer) MouseUp(event *desktop.MouseEvent) {
	if p.bar == nil {
		p.s.release(event)
		return
	}
	if !p.barDragged {
		p.bar.area.Tapped(&fyne.PointEvent{Position: event.Position.Subtract(p.bar.pos), AbsolutePosition: event.AbsolutePosition})
		p.bar = nil
	}
}

func (p *textPointer) Dragged(event *fyne.DragEvent) {
	if p.bar == nil {
		p.s.drag(event)
		return
	}
	p.barDragged = true
	if p.handle != nil {
		p.handle.Dragged(event)
	}
}

func (p *textPointer) DragEnd() {
	if p.bar == nil {
		p.s.endDrag()
		return
	}
	if p.handle != nil {
		p.handle.DragEnd()
	}
	p.bar, p.handle, p.barDragged = nil, nil, false
}

func (p *textPointer) DoubleTapped(event *fyne.PointEvent) {
	if p.s.barAt(event.Position) == nil {
		p.s.selectWord(event)
	}
}

func (p *textPointer) TappedSecondary(event *fyne.PointEvent) { p.s.menu(event) }
func (p *textPointer) MouseIn(event *desktop.MouseEvent)      { p.MouseMoved(event) }

func (p *textPointer) MouseMoved(event *desktop.MouseEvent) {
	p.s.pointerAt = event.Position
	p.s.pointerFrame = p.s.frame()
	bar := p.s.barAt(event.Position)
	var over desktop.Hoverable
	if bar != nil {
		over = bar.area
	}
	if over != p.over {
		p.leaveBar()
		if bar != nil {
			in := *event
			in.Position = event.Position.Subtract(bar.pos)
			over.MouseIn(&in)
		}
		p.over = over
	}
	if bar != nil {
		p.s.unhover()
		return
	}
	p.s.hover(event)
}

func (p *textPointer) MouseOut() {
	p.leaveBar()
	p.s.unhover()
}

func (p *textPointer) leaveBar() {
	if p.over != nil {
		p.over.MouseOut()
		p.over = nil
	}
}
