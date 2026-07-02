//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework Foundation -framework Photos -framework CoreLocation
#include <stdlib.h>

char *photoscrawl_photokit_snapshot(const char *libraryPath, char **errorOut);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"unsafe"
)

type PhotoKitProvider struct{}

func (PhotoKitProvider) Snapshot(ctx context.Context, libraryPath string) (LibrarySnapshot, error) {
	select {
	case <-ctx.Done():
		return LibrarySnapshot{}, ctx.Err()
	default:
	}

	cPath := C.CString(libraryPath)
	defer C.free(unsafe.Pointer(cPath))

	var cErr *C.char
	cJSON := C.photoscrawl_photokit_snapshot(cPath, &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return LibrarySnapshot{}, errors.New(C.GoString(cErr))
	}
	if cJSON == nil {
		return LibrarySnapshot{}, errors.New("PhotoKit returned no snapshot")
	}
	defer C.free(unsafe.Pointer(cJSON))

	var snapshot LibrarySnapshot
	if err := json.Unmarshal([]byte(C.GoString(cJSON)), &snapshot); err != nil {
		return LibrarySnapshot{}, err
	}
	return snapshot, nil
}
