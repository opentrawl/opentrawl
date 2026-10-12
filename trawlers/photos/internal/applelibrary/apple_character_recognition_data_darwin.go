//go:build darwin

package applelibrary

/*
#cgo LDFLAGS: -framework Foundation -framework Vision
#include "apple_character_recognition_native.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

func decodeAppleCharacterRecognitionData(characterRecognitionData []byte) ([]string, error) {
	nativeRecognition := C.decodeAppleCharacterRecognitionRegionTree(
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(characterRecognitionData))),
		C.size_t(len(characterRecognitionData)),
	)
	defer C.freeAppleCharacterRecognitionRegionTree(nativeRecognition)
	if nativeRecognition.failure_message != nil {
		return nil, errors.New(C.GoString(nativeRecognition.failure_message))
	}
	nativeLines := unsafe.Slice(nativeRecognition.recognised_text_lines, int(nativeRecognition.recognised_text_line_count))
	recognisedTextLines := make([]string, len(nativeLines))
	for lineIndex, nativeLine := range nativeLines {
		recognisedTextLines[lineIndex] = string(unsafe.Slice((*byte)(unsafe.Pointer(nativeLine.utf8_bytes)), int(nativeLine.utf8_byte_count)))
	}
	return recognisedTextLines, nil
}
