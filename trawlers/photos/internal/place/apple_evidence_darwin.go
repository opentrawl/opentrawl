//go:build darwin

package place

/*
#cgo darwin LDFLAGS: -framework Foundation -framework CoreLocation -framework MapKit
#include <stdlib.h>

char *photoscrawl_place_evidence_json(const char *requestJSON, char **errorOut);
*/
import "C"

import (
	"context"
	"errors"
	"runtime"
	"unsafe"
)

func callAppleBoundary(ctx context.Context, input Input, radius float64) appleBoundaryOutput {
	requestJSON, err := appleRequestJSON(input, radius)
	if err != nil {
		return appleBoundaryOutput{Err: err}
	}
	select {
	case <-ctx.Done():
		return appleBoundaryOutput{Request: requestJSON, Response: []byte(ctx.Err().Error()), Err: ctx.Err()}
	default:
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cRequest := C.CString(string(requestJSON))
	defer C.free(unsafe.Pointer(cRequest))

	var cErr *C.char
	cJSON := C.photoscrawl_place_evidence_json(cRequest, &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		rawError := []byte(C.GoString(cErr))
		return appleBoundaryOutput{Request: requestJSON, Response: rawError, Err: classifyBridgeError(string(rawError))}
	}
	if cJSON == nil {
		err := errors.New("Apple place evidence returned no JSON")
		return appleBoundaryOutput{Request: requestJSON, Response: []byte(err.Error()), Err: err}
	}
	defer C.free(unsafe.Pointer(cJSON))
	return appleBoundaryOutput{Request: requestJSON, Response: []byte(C.GoString(cJSON))}
}
