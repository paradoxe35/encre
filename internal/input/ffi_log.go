//go:build linux || darwin || windows

package input

/*
#include "bindings.h"

extern void nativeLogGateway(int level, char* message);
*/
import "C"
import (
	"log/slog"

	"github.com/paradoxe35/encre/internal/logger"
)

// ForwardNativeLogs sends what the Rust library and the speech engine report into the app log.
func ForwardNativeLogs() {
	C.encre_log_set_callback(C.encre_LogCallback(C.nativeLogGateway))
}

//export nativeLogGateway
func nativeLogGateway(level C.int, message *C.char) {
	logger.Log(nativeLevel(int(level)), C.GoString(message), "source", "native")
}

// Levels as numbered by the Rust side: debug, info, warn, error.
func nativeLevel(level int) slog.Level {
	switch level {
	case 0:
		return slog.LevelDebug
	case 2:
		return slog.LevelWarn
	case 3:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
