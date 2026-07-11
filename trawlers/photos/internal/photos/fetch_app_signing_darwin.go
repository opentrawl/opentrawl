//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>

int photoscrawl_copy_leaf_certificate(const char *path, unsigned char **bytesOut, long *lengthOut, int *statusOut);
*/
import "C"

import (
	"crypto/sha256"
	"fmt"
	"unsafe"
)

const maxCodeSigningCertificateBytes = 1024 * 1024

func codeSigningLeafDigest(path string) ([sha256.Size]byte, error) {
	certificate, err := codeSigningLeafCertificate(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(certificate), nil
}

func codeSigningLeafCertificate(path string) ([]byte, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var bytes *C.uchar
	var length C.long
	var status C.int
	if C.photoscrawl_copy_leaf_certificate(cPath, &bytes, &length, &status) == 0 {
		return nil, fmt.Errorf("copy code-signing leaf certificate: status %d", int(status))
	}
	defer C.free(unsafe.Pointer(bytes))
	if length <= 0 || length > maxCodeSigningCertificateBytes {
		return nil, fmt.Errorf("code-signing leaf certificate has invalid length %d", int64(length))
	}
	return C.GoBytes(unsafe.Pointer(bytes), C.int(length)), nil
}
