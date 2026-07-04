//go:build darwin

package place

/*
#cgo darwin LDFLAGS: -framework Foundation -framework CoreLocation -framework MapKit
#include <stdlib.h>

char *photoscrawl_place_context_json(const char *requestJSON, char **errorOut);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"
)

func applePlaceContext(ctx context.Context, input Input, radius float64) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	request := struct {
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		RadiusMeters float64 `json:"radius_meters"`
	}{
		Latitude:     input.Location.Latitude,
		Longitude:    input.Location.Longitude,
		RadiusMeters: radius,
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	cRequest := C.CString(string(requestJSON))
	defer C.free(unsafe.Pointer(cRequest))

	var cErr *C.char
	cJSON := C.photoscrawl_place_context_json(cRequest, &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return Result{}, classifyBridgeError(C.GoString(cErr))
	}
	if cJSON == nil {
		return Result{}, errors.New("Apple place context returned no JSON")
	}
	defer C.free(unsafe.Pointer(cJSON))

	var result Result
	if err := json.Unmarshal([]byte(C.GoString(cJSON)), &result); err != nil {
		return Result{}, err
	}
	return result, nil
}

// classifyBridgeError is the single place where the bridge's message strings
// become typed errors. Everything above this boundary uses errors.Is.
func classifyBridgeError(message string) error {
	switch {
	case strings.Contains(message, "MKErrorDomain error 3"):
		return fmt.Errorf("%w: %s", ErrProviderThrottled, message)
	case strings.Contains(message, "timed out"):
		return fmt.Errorf("%w: %s", ErrProviderTimeout, message)
	case strings.Contains(message, "no placemarks"), strings.Contains(message, "no map items"):
		return fmt.Errorf("%w: %s", ErrProviderNoResult, message)
	}
	return errors.New(message)
}
