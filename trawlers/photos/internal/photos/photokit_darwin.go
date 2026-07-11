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
	if err := ctx.Err(); err != nil {
		if cErr != nil {
			C.free(unsafe.Pointer(cErr))
		}
		if cJSON != nil {
			C.free(unsafe.Pointer(cJSON))
		}
		return LibrarySnapshot{}, err
	}
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return LibrarySnapshot{}, photoKitError(C.GoString(cErr))
	}
	if cJSON == nil {
		return LibrarySnapshot{}, errors.New("PhotoKit returned no snapshot")
	}
	defer C.free(unsafe.Pointer(cJSON))

	return decodePhotoKitSnapshot(ctx, []byte(C.GoString(cJSON)))
}

func decodePhotoKitSnapshot(ctx context.Context, snapshotJSON []byte) (LibrarySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return LibrarySnapshot{}, err
	}
	var snapshot LibrarySnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return LibrarySnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return LibrarySnapshot{}, err
	}
	snapshot.Completeness = photoKitSnapshotCompleteness(snapshot.AuthorizationStatus)
	return snapshot, nil
}

func photoKitSnapshotCompleteness(authorizationStatus string) SnapshotCompleteness {
	state := SnapshotPartial
	if authorizationStatus == "authorized" {
		state = SnapshotComplete
	} else if authorizationStatus == "limited" {
		state = SnapshotLimited
	}
	return SnapshotCompleteness{
		State: state,
		Evidence: map[string]string{
			"authorization_status": authorizationStatus,
			"asset_enumeration":    "completed",
		},
	}
}
