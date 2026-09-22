package input

import (
	"log/slog"
	"testing"
)

func TestNativeLevelsMapOntoSlog(t *testing.T) {
	cases := map[int]slog.Level{
		0:  slog.LevelDebug,
		1:  slog.LevelInfo,
		2:  slog.LevelWarn,
		3:  slog.LevelError,
		-1: slog.LevelInfo,
		42: slog.LevelInfo,
	}
	for level, want := range cases {
		if got := nativeLevel(level); got != want {
			t.Errorf("level %d mapped to %v, want %v", level, got, want)
		}
	}
}
