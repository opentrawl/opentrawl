//go:build darwin

package photos

import (
	"errors"
	"testing"
)

func TestCurrentStillNativeFinishOnceRaces(t *testing.T) {
	tests := []struct {
		name                     string
		first, second            int
		started                  bool
		wantCancels, wantSuccess int
	}{
		{name: "callback before timeout", first: 1, second: 2, started: true, wantSuccess: 1},
		{name: "timeout before callback", first: 2, second: 1, started: true, wantCancels: 1},
		{name: "cancellation before callback", first: 3, second: 1, started: true, wantCancels: 1},
		{name: "cancellation before start", first: 3, second: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cancellations, successes := currentStillFinishOnceForTest(test.first, test.second, test.started)
			if cancellations != test.wantCancels || successes != test.wantSuccess {
				t.Fatalf("cancellations=%d successes=%d, want %d %d", cancellations, successes, test.wantCancels, test.wantSuccess)
			}
		})
	}
}

func TestCurrentStillNativeCancellationBeforeRegistrationStartsNoRequest(t *testing.T) {
	if !currentStillCancelBeforeRegistrationForTest() {
		t.Fatal("cancellation before registration did not stop request start")
	}
}

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
