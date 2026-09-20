# Why this copy exists

`fyne.io/systray` at `v1.12.3-0.20260810170012-af4e8e793ec4`, with one change in
`systray_unix.go`: `argbForImage` converts pixels through `color.NRGBAModel`.

Upstream reads `img.At(x, y).RGBA()`, which is 16-bit and premultiplied, and
keeps only the low byte of each channel. Fully opaque pixels survive, but every
anti-aliased edge pixel is sent with a garbage colour, so the Linux tray icon
gets a dirty, jagged outline.

`systray_icon_unix_test.go` covers the fix. Drop the `replace` in `go.mod` and
this directory once the fix lands upstream.
