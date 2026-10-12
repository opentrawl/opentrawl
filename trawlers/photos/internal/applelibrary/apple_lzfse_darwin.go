//go:build darwin

package applelibrary

/*
#cgo LDFLAGS: -lcompression
#include <compression.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// decompressAppleLZFSE uses macOS's own libcompression, which Apple used to
// compress the data.
func decompressAppleLZFSE(compressedData []byte, uncompressedByteCount int) ([]byte, error) {
	if len(compressedData) == 0 || uncompressedByteCount <= 0 {
		return nil, fmt.Errorf("LZFSE data is empty")
	}
	uncompressedData := make([]byte, uncompressedByteCount)
	decodedByteCount := C.compression_decode_buffer(
		(*C.uint8_t)(unsafe.Pointer(&uncompressedData[0])), C.size_t(len(uncompressedData)),
		(*C.uint8_t)(unsafe.Pointer(&compressedData[0])), C.size_t(len(compressedData)),
		nil, C.COMPRESSION_LZFSE,
	)
	if int(decodedByteCount) != uncompressedByteCount {
		return nil, fmt.Errorf("LZFSE data decoded to %d bytes, not %d", int(decodedByteCount), uncompressedByteCount)
	}
	return uncompressedData, nil
}
