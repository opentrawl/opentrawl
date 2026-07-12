//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework Foundation -framework Photos -framework ImageIO
#include <stdlib.h>
int photoscrawl_export_current_still_matching(const char *assetUUID, long long modificationUnixSeconds, int modificationMicroseconds, const char *destinationPath, int allowNetwork, long long timeoutMilliseconds, char **mediaTypeOut, long long *orientationOut, long long *pixelWidthOut, long long *pixelHeightOut, char **errorOut, char **errorDomainOut, long long *errorCodeOut, int *callbackCancelledOut, int *callbackDegradedOut, int *callbackInCloudOut, int *callbackReturnedOut, char **stageOut);
int photoscrawl_prepare_current_still_main_loop(void);
void photoscrawl_run_current_still_main_loop(void);
void photoscrawl_stop_current_still_main_loop(void);
void photoscrawl_cancel_current_still_request(void);
int photoscrawl_test_current_still_finish_once(int first, int second, int started, int *cancelCountOut, int *successCountOut);
int photoscrawl_test_current_still_cancel_before_registration(void);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

func PrepareCurrentStillMainLoop() bool { return C.photoscrawl_prepare_current_still_main_loop() != 0 }

func RunCurrentStillMainLoop() { C.photoscrawl_run_current_still_main_loop() }

func StopCurrentStillMainLoop() { C.photoscrawl_stop_current_still_main_loop() }

func currentStillFinishOnceForTest(first, second int, started bool) (int, int) {
	var cancellations, successes C.int
	C.photoscrawl_test_current_still_finish_once(C.int(first), C.int(second), C.int(boolInt(started)), &cancellations, &successes)
	return int(cancellations), int(successes)
}

func currentStillCancelBeforeRegistrationForTest() bool {
	return C.photoscrawl_test_current_still_cancel_before_registration() != 0
}

// ExportCurrentStillMatching obtains only the full-resolution .current image
// from PhotoKit. The callback does not install a file after timeout.
func ExportCurrentStillMatching(ctx context.Context, request CurrentStillRequest, destinationPath string) (CurrentStillFact, error) {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return CurrentStillFact{}, NewCurrentStillStageError(CurrentStillStagePrepareDestination, err)
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
	cDestination := C.CString(temporary)
	defer C.free(unsafe.Pointer(cDestination))
	var mediaType *C.char
	var orientation, width, height C.longlong
	var cErr, domain, stage *C.char
	var code C.longlong
	var cancelled, degraded, inCloud, callbackReturned C.int
	nativeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			C.photoscrawl_cancel_current_still_request()
		case <-nativeDone:
		}
	}()
	ok := C.photoscrawl_export_current_still_matching(cUUID, C.longlong(request.Modification.UnixSeconds), C.int(request.Modification.Microseconds), cDestination, C.int(boolInt(request.AllowNetwork)), C.longlong(timeout.Milliseconds()), &mediaType, &orientation, &width, &height, &cErr, &domain, &code, &cancelled, &degraded, &inCloud, &callbackReturned, &stage)
	close(nativeDone)
	if mediaType != nil {
		defer C.free(unsafe.Pointer(mediaType))
	}
	if domain != nil {
		defer C.free(unsafe.Pointer(domain))
	}
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
	}
	if stage != nil {
		defer C.free(unsafe.Pointer(stage))
	}
	if err := ctx.Err(); err != nil {
		return CurrentStillFact{}, err
	}
	domainText := ""
	if domain != nil {
		domainText = C.GoString(domain)
	}
	reason := ""
	if cErr != nil {
		reason = C.GoString(cErr)
	}
	stageText := ""
	if stage != nil {
		stageText = C.GoString(stage)
	}
	if ok == 0 {
		if reason == "" {
			reason = "PhotoKit current-still callback did not produce a final image"
		}
		return CurrentStillFact{}, currentStillBridgeError(domainText, int64(code), reason, cancelled != 0, degraded != 0, inCloud != 0, callbackReturned != 0, stageText)
	}
	if err := os.Rename(temporary, destinationPath); err != nil {
		return CurrentStillFact{}, NewCurrentStillStageError(CurrentStillStageRenameOutput, err)
	}
	info, digest, err := InspectOriginalFile(destinationPath)
	if err != nil {
		return CurrentStillFact{}, NewCurrentStillStageError(CurrentStillStageInspectOutput, err)
	}
	return CurrentStillFact{MediaType: C.GoString(mediaType), Orientation: int32(orientation), PixelWidth: int64(width), PixelHeight: int64(height), Size: info.Size(), SHA256: fmt.Sprintf("%x", digest)}, nil
}

func currentStillBridgeError(domain string, code int64, reason string, cancelled, degraded, inCloud, callbackReturned bool, stage string) error {
	typed := photoKitError(reason)
	if IsPhotoLibraryAccessError(typed) || errors.Is(typed, ErrPhotoKitAssetNotFound) {
		return typed
	}
	hasCallbackFacts := domain != "" || code != 0 || cancelled || degraded || inCloud || callbackReturned
	if errors.Is(typed, ErrPhotoKitExportTimedOut) {
		if hasCallbackFacts {
			return NewPhotoKitCallbackTimeoutError(domain, code, reason, cancelled, degraded, inCloud)
		}
		return typed
	}
	if hasCallbackFacts {
		return NewPhotoKitCallbackError(domain, code, reason, cancelled, degraded, inCloud, callbackReturned)
	}
	if isCurrentStillStage(stage) {
		return NewCurrentStillStageError(stage, typed)
	}
	return typed
}
