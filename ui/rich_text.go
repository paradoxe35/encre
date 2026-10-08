package ui

import (
	"reflect"
	"unsafe"

	"fyne.io/fyne/v2/widget"
)

// adopt hands text nested in lists the theme of the rich text it shows in. Fyne only does so for
// top-level text, leaving the rest in the app's theme, so lists become paragraph blocks made once
// and their text is told which rich text holds it.
func adopt(holder *widget.RichText, segments []widget.RichTextSegment) []widget.RichTextSegment {
	adopted := make([]widget.RichTextSegment, len(segments))
	for i, segment := range segments {
		switch segment := segment.(type) {
		case *widget.ListSegment:
			adopted[i] = &widget.ParagraphSegment{Texts: adopt(holder, segment.Segments())}
		case *widget.ParagraphSegment:
			segment.Texts = adopt(holder, segment.Texts)
			adopted[i] = segment
		case *widget.TextSegment:
			setHolder(segment, holder)
			adopted[i] = segment
		default:
			adopted[i] = segment
		}
	}
	return adopted
}

// eachText runs on every text segment, those nested in blocks included.
func eachText(segments []widget.RichTextSegment, do func(*widget.TextSegment)) {
	for _, segment := range segments {
		switch segment := segment.(type) {
		case *widget.TextSegment:
			do(segment)
		case widget.RichTextBlock:
			eachText(segment.Segments(), do)
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
