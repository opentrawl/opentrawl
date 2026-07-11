//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework Foundation -framework Photos -framework ImageIO
#include <stdlib.h>
int photoscrawl_export_current_still_matching(const char *assetUUID, const char *modificationDate, const char *destinationPath, int allowNetwork, long long timeoutMilliseconds, char **mediaTypeOut, long long *orientationOut, long long *pixelWidthOut, long long *pixelHeightOut, char **errorOut, char **errorDomainOut, long long *errorCodeOut);
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

// ExportCurrentStillMatching obtains only the full-resolution .current image
// from PhotoKit. The callback does not install a file after timeout.
func ExportCurrentStillMatching(ctx context.Context, request CurrentStillRequest, destinationPath string) (CurrentStillFact, error) {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return CurrentStillFact{}, err
	}
	timeout := defaultPhotoKitFetchTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return CurrentStillFact{}, context.DeadlineExceeded
		}
	}
	temporary := destinationPath + ".exporting"
	_ = os.Remove(temporary)
	defer func() { _ = os.Remove(temporary) }()
	cUUID := C.CString(request.AssetUUID)
	defer C.free(unsafe.Pointer(cUUID))
	cModification := C.CString(request.ModificationDate)
	defer C.free(unsafe.Pointer(cModification))
	cDestination := C.CString(temporary)
	defer C.free(unsafe.Pointer(cDestination))
	var mediaType *C.char
	var orientation, width, height C.longlong
	var cErr, domain *C.char
	var code C.longlong
	ok := C.photoscrawl_export_current_still_matching(cUUID, cModification, cDestination, C.int(boolInt(request.AllowNetwork)), C.longlong(timeout.Milliseconds()), &mediaType, &orientation, &width, &height, &cErr, &domain, &code)
	if mediaType != nil {
		defer C.free(unsafe.Pointer(mediaType))
	}
	if domain != nil {
		defer C.free(unsafe.Pointer(domain))
	}
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
	}
	if domain != nil {
		return CurrentStillFact{}, NewPhotoKitExportError(C.GoString(domain), int64(code), C.GoString(cErr))
	}
	if cErr != nil {
		return CurrentStillFact{}, photoKitError(C.GoString(cErr))
	}
	if ok == 0 {
		return CurrentStillFact{}, fmt.Errorf("PhotoKit current-still request failed")
	}
	if err := os.Rename(temporary, destinationPath); err != nil {
		return CurrentStillFact{}, err
	}
	info, digest, err := InspectOriginalFile(destinationPath)
	if err != nil {
		return CurrentStillFact{}, err
	}
	return CurrentStillFact{MediaType: C.GoString(mediaType), Orientation: int32(orientation), PixelWidth: int64(width), PixelHeight: int64(height), Size: info.Size(), SHA256: fmt.Sprintf("%x", digest)}, nil
}
