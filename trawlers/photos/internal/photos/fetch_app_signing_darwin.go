//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>

int photoscrawl_copy_leaf_certificate(const char *path, unsigned char **bytesOut, long *lengthOut, int *statusOut);
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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

func codeSigningLeafIdentityMatches(ctx context.Context, callerPath, helperPath string) (bool, error) {
	callerDigest, callerErr := codeSigningLeafDigest(callerPath)
	helperDigest, helperErr := codeSigningLeafDigest(helperPath)
	if callerErr == nil && helperErr == nil {
		return callerDigest == helperDigest, nil
	}

	callerRequirement, err := fixedLeafRequirementHash(ctx, callerPath)
	if err != nil {
		if callerErr != nil {
			err = callerErr
		}
		return false, fmt.Errorf("read Photos helper caller leaf certificate identity: %w", err)
	}
	helperRequirement, err := fixedLeafRequirementHash(ctx, helperPath)
	if err != nil {
		if helperErr != nil {
			err = helperErr
		}
		return false, fmt.Errorf("read signed Photos helper leaf certificate identity: %w", err)
	}
	return callerRequirement == helperRequirement, nil
}

func fixedLeafRequirementHash(ctx context.Context, path string) (string, error) {
	output, err := runPhotoKitFetchCombinedCommand(ctx, "/usr/bin/codesign", "--display", "--requirements", "-", path)
	if err != nil {
		return "", err
	}
	return parseFixedLeafRequirementHash(string(output))
}

func parseFixedLeafRequirementHash(text string) (string, error) {
	const marker = `certificate leaf = H"`
	start := strings.Index(text, marker)
	if start < 0 || strings.Index(text[start+len(marker):], marker) >= 0 {
		return "", fmt.Errorf("code-signing requirement does not contain one fixed leaf certificate")
	}
	start += len(marker)
	end := strings.IndexByte(text[start:], '"')
	if end < 0 {
		return "", fmt.Errorf("code-signing leaf requirement is unterminated")
	}
	value := text[start : start+end]
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 20 {
		return "", fmt.Errorf("code-signing leaf requirement is invalid")
	}
	return strings.ToLower(value), nil
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
