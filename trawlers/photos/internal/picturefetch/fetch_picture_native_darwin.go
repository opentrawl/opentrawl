//go:build darwin

package picturefetch

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework AVFoundation -framework CoreGraphics -framework CoreMedia -framework ImageIO -framework Photos
#include <stdlib.h>
#include "fetch_picture_native.h"
*/
import "C"

import (
	"unsafe"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

func fetchUprightPictureFromFile(pictureFilePath string, pictureFileIsVideo bool, pictureLongEdgePixelLimit int32) (fetchedUprightPicture, error) {
	nativePictureFilePath := C.CString(pictureFilePath)
	defer C.free(unsafe.Pointer(nativePictureFilePath))
	return fetchedUprightPictureFromNative(C.fetchUprightPictureFromFile(nativePictureFilePath, C.bool(pictureFileIsVideo), C.int32_t(pictureLongEdgePixelLimit)))
}

func fetchUprightPictureFromApplePhotos(applePhotoUUID string, pictureLongEdgePixelLimit int32) (fetchedUprightPicture, error) {
	nativeApplePhotoUUID := C.CString(applePhotoUUID)
	defer C.free(unsafe.Pointer(nativeApplePhotoUUID))
	return fetchedUprightPictureFromNative(C.fetchUprightPictureFromApplePhotos(nativeApplePhotoUUID, C.int32_t(pictureLongEdgePixelLimit)))
}

func fetchedUprightPictureFromNative(nativePicture C.NativeFetchedPicture) (fetchedUprightPicture, error) {
	defer C.freeNativeFetchedPicture(nativePicture)
	switch nativePicture.picture_fetch_outcome {
	case C.NATIVE_PICTURE_FETCH_OUTCOME_FETCHED:
	case C.NATIVE_PICTURE_FETCH_OUTCOME_APPLE_PHOTOS_ACCESS_NOT_GRANTED:
		return fetchedUprightPicture{}, ErrApplePhotosAccessNotGranted
	default:
		return fetchedUprightPicture{}, &pictureFetchFailure{
			pictureFetchFailureMessage: C.GoString(nativePicture.picture_fetch_failure_message),
			failureMayPassOnRetry:      nativePicture.picture_fetch_outcome == C.NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
		}
	}
	fetchedPicture := fetchedUprightPicture{
		pictureJPEGBytes: C.GoBytes(unsafe.Pointer(nativePicture.picture_jpeg_bytes), C.int(nativePicture.picture_jpeg_byte_count)),
	}
	if bool(nativePicture.camera_bearing_is_recorded) {
		cameraBearingDegrees := float64(nativePicture.camera_bearing_degrees)
		fetchedPicture.cameraBearingDegrees = &cameraBearingDegrees
	}
	switch nativePicture.camera_bearing_reference_letter {
	case 'T':
		fetchedPicture.cameraBearingReference = photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_TRUE_NORTH
	case 'M':
		fetchedPicture.cameraBearingReference = photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_MAGNETIC_NORTH
	}
	return fetchedPicture, nil
}
