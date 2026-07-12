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
	Domain            string
	Code              int64
	Reason            string
	CallbackCancelled bool
	CallbackDegraded  bool
	CallbackInCloud   bool
	CallbackTimedOut  bool
	CallbackReturned  bool
}

const (
	CurrentStillStageSelectionValidation = "selection_validation"
	CurrentStillStageImageDecode         = "image_decode"
	CurrentStillStageImageDimensions     = "image_dimensions"
	CurrentStillStageOutputWrite         = "output_write"
	CurrentStillStagePrepareDestination  = "prepare_destination"
	CurrentStillStageRenameOutput        = "rename_output"
	CurrentStillStageInspectOutput       = "inspect_output"
)

// CurrentStillStageError records a fixed, non-sensitive export stage. The
// underlying error remains available to Go callers, but is never returned on
// the signed helper wire.
type CurrentStillStageError struct {
	stage string
	err   error
}

func NewCurrentStillStageError(stage string, err error) error {
	if err == nil || !isCurrentStillStage(stage) {
		return err
	}
	return &CurrentStillStageError{stage: stage, err: err}
}

func (e *CurrentStillStageError) Error() string {
	return "PhotoKit current-still export failed"
}

func (e *CurrentStillStageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *CurrentStillStageError) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}

func isCurrentStillStage(stage string) bool {
	switch stage {
	case CurrentStillStageSelectionValidation,
		CurrentStillStageImageDecode,
		CurrentStillStageImageDimensions,
		CurrentStillStageOutputWrite,
		CurrentStillStagePrepareDestination,
		CurrentStillStageRenameOutput,
		CurrentStillStageInspectOutput:
		return true
	default:
		return false
	}
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

// NewPhotoKitCallbackError keeps the callback facts that explain why a
// current-still request did not produce a final image. The facts are booleans,
// so they are safe to return from the signed helper and to write to logs.
func NewPhotoKitCallbackError(domain string, code int64, reason string, cancelled, degraded, inCloud, returned bool) *PhotoKitExportError {
	result := NewPhotoKitExportError(domain, code, reason)
	result.CallbackCancelled = cancelled
	result.CallbackDegraded = degraded
	result.CallbackInCloud = inCloud
	result.CallbackReturned = returned
	return result
}

func NewPhotoKitCallbackTimeoutError(domain string, code int64, reason string, cancelled, degraded, inCloud bool) *PhotoKitExportError {
	result := NewPhotoKitCallbackError(domain, code, reason, cancelled, degraded, inCloud, false)
	result.CallbackTimedOut = true
	return result
}

func (e *PhotoKitExportError) Unwrap() error {
	if e != nil && e.CallbackTimedOut {
		return ErrPhotoKitExportTimedOut
	}
	return nil
}

func (e *PhotoKitExportError) CallbackFacts() string {
	if e == nil {
		return "cancelled=false degraded=false in_cloud=false timed_out=false callback_returned=false"
	}
	return fmt.Sprintf("cancelled=%t degraded=%t in_cloud=%t timed_out=%t callback_returned=%t", e.CallbackCancelled, e.CallbackDegraded, e.CallbackInCloud, e.CallbackTimedOut, e.CallbackReturned)
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
