//go:build darwin

package platform

/*
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
#include <stdlib.h>
void encre_toast(const char* title, const char* body);
*/
import "C"

import (
	"fmt"
	"os/exec"
	"strings"
	"unsafe"

	"github.com/paradoxe35/encre/internal/logger"
)

// Toast shows a notification as Encre, with its icon, once the user has allowed it, and through
// AppleScript otherwise: that one shows without the icon but needs no permission, so a refusal or a
// reinstall never silences Encre.
func Toast(_, title, content string) bool {
	cTitle, cContent := C.CString(title), C.CString(content)
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cContent))
	C.encre_toast(cTitle, cContent)
	return true
}

//export encreToastByScript
func encreToastByScript(title, content *C.char) {
	script := fmt.Sprintf("display notification %s with title %s",
		appleScriptString(C.GoString(content)), appleScriptString(C.GoString(title)))
	go func() {
		if err := exec.Command("osascript", "-e", script).Run(); err != nil {
			logger.Warn("Could not show a notification", "error", err)
		}
	}()
}

func appleScriptString(text string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
}
