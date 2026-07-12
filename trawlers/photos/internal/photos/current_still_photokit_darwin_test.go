//go:build darwin

package photos

import (
	"errors"
	"testing"
)

func TestCurrentStillBridgeErrorPreservesTerminalCallbackFacts(t *testing.T) {
	err := currentStillBridgeError("PHPhotosErrorDomain", 3303, "callback /private/source", true, true, false, true, "")
	var callbackErr *PhotoKitExportError
	if !errors.As(err, &callbackErr) {
		t.Fatalf("error type = %T, want PhotoKitExportError", err)
	}
	if callbackErr.Domain != "PHPhotosErrorDomain" || callbackErr.Code != 3303 || !callbackErr.CallbackCancelled || !callbackErr.CallbackDegraded || callbackErr.CallbackInCloud || !callbackErr.CallbackReturned {
		t.Fatalf("callback error = %#v", callbackErr)
	}
	if callbackErr.Reason != "PhotoKit could not export the selected camera original" {
		t.Fatalf("callback reason = %q", callbackErr.Reason)
	}
}

func TestCurrentStillBridgeErrorPreservesTimeoutFacts(t *testing.T) {
	err := currentStillBridgeError("", 0, ErrPhotoKitExportTimedOut.Error(), false, true, true, false, "")
	if !errors.Is(err, ErrPhotoKitExportTimedOut) {
		t.Fatalf("error = %v, want timeout", err)
	}
	var callbackErr *PhotoKitExportError
	if !errors.As(err, &callbackErr) || !callbackErr.CallbackTimedOut || !callbackErr.CallbackDegraded || !callbackErr.CallbackInCloud {
		t.Fatalf("callback error = %#v", callbackErr)
	}
}

func TestCurrentStillBridgeErrorMarksTerminalCallbackWithoutNativeError(t *testing.T) {
	err := currentStillBridgeError("", 0, "PhotoKit current-still callback did not return a final image", false, false, false, true, "")
	var callbackErr *PhotoKitExportError
	if !errors.As(err, &callbackErr) || !callbackErr.CallbackReturned {
		t.Fatalf("callback error = %#v", callbackErr)
	}
}

func TestCurrentStillBridgeErrorMarksGenericNativeStages(t *testing.T) {
	for _, stage := range []string{
		CurrentStillStageSelectionValidation,
		CurrentStillStageImageDecode,
		CurrentStillStageImageDimensions,
		CurrentStillStageOutputWrite,
	} {
		err := currentStillBridgeError("", 0, "native stage /private/source", false, false, false, false, stage)
		var stageErr *CurrentStillStageError
		if !errors.As(err, &stageErr) || stageErr.Stage() != stage {
			t.Fatalf("stage=%q error=%T %#v", stage, err, stageErr)
		}
	}
}
