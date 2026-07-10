package photos

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrPhotoKitAssetNotFound  = errors.New("photokit asset not found")
	ErrPhotoKitExportTimedOut = errors.New("photokit original export timed out")
)

type PhotoKitExportError struct {
	Domain string
	Code   int64
	Reason string
}

func (e *PhotoKitExportError) Error() string {
	if e == nil {
		return "PhotoKit export failed"
	}
	return fmt.Sprintf("PhotoKit export failed (domain=%s code=%d): %s", safePhotoKitDomain(e.Domain), e.Code, safePhotoKitReason(e.Reason))
}

func NewPhotoKitExportError(domain string, code int64, reason string) *PhotoKitExportError {
	return &PhotoKitExportError{
		Domain: safePhotoKitDomain(domain),
		Code:   code,
		Reason: safePhotoKitReason(reason),
	}
}

func safePhotoKitDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" || len(domain) > 128 {
		return "unknown"
	}
	for _, r := range domain {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return "unknown"
	}
	return domain
}

func safePhotoKitReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 160 || strings.ContainsAny(reason, "/\\\r\n") {
		return "PhotoKit could not export the selected camera original"
	}
	return reason
}
