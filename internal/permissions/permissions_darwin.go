//go:build darwin

package permissions

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices -framework AVFoundation
#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <AVFoundation/AVFoundation.h>

// Both checks are silent: the card on screen replaces the system prompt.
bool IsAccessibilityTrusted(void) {
    return AXIsProcessTrusted();
}

bool HasInputMonitoringPermission(void) {
    return CGPreflightListenEventAccess();
}

// Reading the status never prompts; only a capture request does.
bool IsMicrophoneDenied(void) {
    AVAuthorizationStatus status = [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
    return status == AVAuthorizationStatusDenied || status == AVAuthorizationStatusRestricted;
}

static void OpenPrivacyPane(NSString *pane) {
    NSString *urlString = [@"x-apple.systempreferences:com.apple.preference.security?" stringByAppendingString:pane];
    [[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:urlString]];
}

void OpenAccessibilityPreferences(void) {
    OpenPrivacyPane(@"Privacy_Accessibility");
}

void OpenInputMonitoringPreferences(void) {
    OpenPrivacyPane(@"Privacy_ListenEvent");
}

void OpenMicrophonePreferences(void) {
    OpenPrivacyPane(@"Privacy_Microphone");
}
*/
import "C"

func IsSupported() bool {
	return true
}

func CurrentState() State {
	return State{
		AccessibilityGranted:   bool(C.IsAccessibilityTrusted()),
		InputMonitoringGranted: bool(C.HasInputMonitoringPermission()),
		MicrophoneDenied:       bool(C.IsMicrophoneDenied()),
	}
}

func OpenPreference(t Type) {
	switch t {
	case Accessibility:
		C.OpenAccessibilityPreferences()
	case InputMonitoring:
		C.OpenInputMonitoringPreferences()
	case Microphone:
		C.OpenMicrophonePreferences()
	}
}
