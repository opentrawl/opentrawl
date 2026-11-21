//go:build darwin

package cli

/*
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>

int opentrawl_source_app_is_running(const char *bundle_identifier);
*/
import "C"
import "unsafe"

func sourceAppBundleIsRunning(bundleIdentifier string) bool {
	if bundleIdentifier == "" {
		return false
	}
	cBundleIdentifier := C.CString(bundleIdentifier)
	defer C.free(unsafe.Pointer(cBundleIdentifier))
	return C.opentrawl_source_app_is_running(cBundleIdentifier) != 0
}
