//go:build darwin

package photos

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos/fetchwire"
	"google.golang.org/protobuf/proto"
)

func TestExportOriginalResourceThroughAppChecksWireInputAndOutput(t *testing.T) {
	oldLaunch := launchPhotoKitFetchApp
	launchPhotoKitFetchApp = func(_ context.Context, requestPath, responsePath string) error {
		requestData, err := os.ReadFile(requestPath)
		if err != nil {
			t.Fatal(err)
		}
		var request fetchwire.OriginalFetchRequest
		if err := proto.Unmarshal(requestData, &request); err != nil {
			t.Fatal(err)
		}
		if request.LocalIdentifier != "synthetic-asset" ||
			request.CreationDate != "2026-07-10T12:00:00Z" ||
			request.Width != 4032 || request.Height != 3024 ||
			request.OriginalFilename != "synthetic.heic" || !request.AllowNetwork ||
			request.TimeoutMilliseconds <= 0 {
			t.Fatalf("request = %#v", request)
		}
		const original = "exact synthetic original bytes"
		if err := os.WriteFile(request.DestinationPath, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(original))
		responseData, err := proto.Marshal(&fetchwire.OriginalFetchResponse{
			Success:   true,
			SizeBytes: int64(len(original)),
			Sha256:    digest[:],
		})
		if err != nil {
			t.Fatal(err)
		}
		return os.WriteFile(responsePath, responseData, 0o600)
	}
	defer func() { launchPhotoKitFetchApp = oldLaunch }()

	destination := filepath.Join(t.TempDir(), "original.heic")
	err := ExportOriginalResourceThroughApp(context.Background(), OriginalExportQuery{
		LocalIdentifier:  "synthetic-asset",
		CreationDate:     "2026-07-10T12:00:00Z",
		Width:            4032,
		Height:           3024,
		OriginalFilename: "synthetic.heic",
	}, destination, true)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "exact synthetic original bytes" {
		t.Fatalf("output = %q err = %v", data, err)
	}
}

func TestAssetReadinessThroughAppChecksTypedResponse(t *testing.T) {
	oldResolve := resolvePhotoKitFetchApp
	oldLaunch := launchPhotoKitReadinessApp
	t.Cleanup(func() {
		resolvePhotoKitFetchApp = oldResolve
		launchPhotoKitReadinessApp = oldLaunch
	})
	resolvePhotoKitFetchApp = func(context.Context) (string, error) { return "/synthetic/Photoscrawl Fetch.app", nil }
	launchPhotoKitReadinessApp = func(_ context.Context, appPath, requestPath, responsePath string) error {
		if appPath != "/synthetic/Photoscrawl Fetch.app" {
			t.Fatalf("app path = %q", appPath)
		}
		data, err := os.ReadFile(requestPath)
		if err != nil {
			return err
		}
		var request fetchwire.AssetReadinessRequest
		if err := proto.Unmarshal(data, &request); err != nil {
			return err
		}
		if request.AssetUuid != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE" {
			t.Fatalf("readiness request UUID = %q", request.AssetUuid)
		}
		response, err := proto.Marshal(&fetchwire.AssetReadinessResponse{
			Success: true, LocalIdentifier: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001", AssetUuid: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
			MediaType: "image", CreationDate: "2026-07-14T12:00:00Z", ModificationDate: "2026-07-14T12:00:01Z",
			PixelWidth: 4032, PixelHeight: 3024, OriginalFilename: "synthetic.heic", OriginalUti: "public.heic",
		})
		if err != nil {
			return err
		}
		return os.WriteFile(responsePath, response, 0o600)
	}
	readiness, err := AssetReadinessThroughApp(context.Background(), "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE")
	if err != nil {
		t.Fatal(err)
	}
	if readiness.AssetUUID != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE" || readiness.LocalIdentifier == "" || readiness.OriginalFilename != "synthetic.heic" || readiness.HasLocation {
		t.Fatalf("readiness = %#v", readiness)
	}
}

func TestExportCurrentStillThroughAppStopsBeforeLaunchAfterCancellation(t *testing.T) {
	oldLaunch := launchPhotoKitCurrentStillApp
	defer func() { launchPhotoKitCurrentStillApp = oldLaunch }()
	launched := false
	launchPhotoKitCurrentStillApp = func(context.Context, string, string, string) error { launched = true; return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ExportCurrentStillThroughApp(ctx, testCurrentStillRequest(t), filepath.Join(t.TempDir(), "current.heic"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if launched {
		t.Fatal("cancelled current-still request launched the helper")
	}
}

func TestExportCurrentStillThroughAppReturnsEveryMeasuredHelperPhase(t *testing.T) {
	oldResolve := resolvePhotoKitFetchApp
	oldLaunch := launchPhotoKitCurrentStillApp
	defer func() {
		resolvePhotoKitFetchApp = oldResolve
		launchPhotoKitCurrentStillApp = oldLaunch
	}()
	resolvePhotoKitFetchApp = func(context.Context) (string, error) {
		time.Sleep(time.Millisecond)
		return "/synthetic/Photoscrawl Fetch.app", nil
	}
	launchPhotoKitCurrentStillApp = func(_ context.Context, appPath, requestPath, responsePath string) error {
		if appPath != "/synthetic/Photoscrawl Fetch.app" {
			t.Fatalf("app path = %q", appPath)
		}
		requestData, err := os.ReadFile(requestPath)
		if err != nil {
			t.Fatal(err)
		}
		var request fetchwire.CurrentStillFetchRequest
		if err := proto.Unmarshal(requestData, &request); err != nil {
			t.Fatal(err)
		}
		if !request.HasExpectedModification || request.ModificationUnixSeconds != 1783771200 || request.ModificationMicroseconds != 123000 {
			t.Fatalf("wire request modification state = %#v", request)
		}
		time.Sleep(time.Millisecond)
		const current = "exact synthetic current-still bytes"
		if err := os.WriteFile(request.DestinationPath, []byte(current), 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(current))
		responseData, err := proto.Marshal(&fetchwire.CurrentStillFetchResponse{
			Success:                true,
			SizeBytes:              int64(len(current)),
			Sha256:                 digest[:],
			MediaType:              "public.jpeg",
			Orientation:            1,
			PixelWidth:             2,
			PixelHeight:            3,
			PhotokitCallbackMicros: 101,
			ValidationHashMicros:   102,
			PhotokitCalls:          1,
			HelperStartedUnixNanos: time.Now().UnixNano(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return os.WriteFile(responsePath, responseData, 0o600)
	}
	destination := filepath.Join(t.TempDir(), "current.jpeg")
	fact, err := ExportCurrentStillThroughApp(context.Background(), testCurrentStillRequest(t), destination)
	if err != nil {
		t.Fatal(err)
	}
	if fact.PhotoKitCalls != 1 || fact.Timings.HelperVerificationMicros <= 0 || fact.Timings.LaunchServicesStartMicros <= 0 || fact.Timings.PhotoKitCallbackMicros != 101 || fact.Timings.ValidationHashMicros < 102 {
		t.Fatalf("timings = %#v", fact.Timings)
	}
}

func TestExportCurrentStillThroughAppSendsNoFabricatedModification(t *testing.T) {
	oldResolve := resolvePhotoKitFetchApp
	oldLaunch := launchPhotoKitCurrentStillApp
	defer func() {
		resolvePhotoKitFetchApp = oldResolve
		launchPhotoKitCurrentStillApp = oldLaunch
	}()
	resolvePhotoKitFetchApp = func(context.Context) (string, error) {
		return "/synthetic/Photoscrawl Fetch.app", nil
	}
	var wireRequest fetchwire.CurrentStillFetchRequest
	launchPhotoKitCurrentStillApp = func(_ context.Context, _, requestPath, _ string) error {
		requestData, err := os.ReadFile(requestPath)
		if err != nil {
			return err
		}
		t.Logf("boundary=synthetic_source_fingerprint_wire_request raw_hex=%x", requestData)
		if err := proto.Unmarshal(requestData, &wireRequest); err != nil {
			return err
		}
		return errors.New("synthetic stop after wire capture")
	}
	freshness, err := CurrentStillFreshnessForSourceFingerprint("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	request := CurrentStillRequest{SourceLibraryID: "synthetic-library", AssetUUID: "synthetic-asset", Freshness: freshness}
	if _, err := ExportCurrentStillThroughApp(context.Background(), request, filepath.Join(t.TempDir(), "current.heic")); err == nil {
		t.Fatal("synthetic launch stop did not surface")
	}
	if wireRequest.HasExpectedModification || wireRequest.ModificationUnixSeconds != 0 || wireRequest.ModificationMicroseconds != 0 {
		t.Fatalf("wire request fabricated a modification instant: %#v", wireRequest)
	}
}

func TestExportCurrentStillThroughAppMeasuresEveryFailureBoundary(t *testing.T) {
	oldResolve := resolvePhotoKitFetchApp
	oldLaunch := launchPhotoKitCurrentStillApp
	defer func() {
		resolvePhotoKitFetchApp = oldResolve
		launchPhotoKitCurrentStillApp = oldLaunch
	}()
	resolvePhotoKitFetchApp = func(context.Context) (string, error) {
		time.Sleep(time.Millisecond)
		return "/synthetic/Photoscrawl Fetch.app", nil
	}
	request := testCurrentStillRequest(t)

	t.Run("launch", func(t *testing.T) {
		launchPhotoKitCurrentStillApp = func(context.Context, string, string, string) error {
			time.Sleep(time.Millisecond)
			return errors.New("synthetic launch failure")
		}
		_, err := ExportCurrentStillThroughApp(context.Background(), request, filepath.Join(t.TempDir(), "current.heic"))
		assertMeasuredFactFailure(t, err, 0, func(timings CurrentStillPhaseTimings) bool {
			return timings.HelperVerificationMicros > 0 && timings.LaunchServicesStartMicros > 0
		})
	})

	t.Run("read", func(t *testing.T) {
		launchPhotoKitCurrentStillApp = func(context.Context, string, string, string) error { return nil }
		_, err := ExportCurrentStillThroughApp(context.Background(), request, filepath.Join(t.TempDir(), "current.heic"))
		assertMeasuredFactFailure(t, err, 0, func(timings CurrentStillPhaseTimings) bool {
			return timings.HelperVerificationMicros > 0 && timings.LaunchServicesStartMicros > 0
		})
	})

	t.Run("decode", func(t *testing.T) {
		launchPhotoKitCurrentStillApp = func(_ context.Context, _, _, responsePath string) error {
			return os.WriteFile(responsePath, []byte{0xff}, 0o600)
		}
		_, err := ExportCurrentStillThroughApp(context.Background(), request, filepath.Join(t.TempDir(), "current.heic"))
		assertMeasuredFactFailure(t, err, 0, func(timings CurrentStillPhaseTimings) bool {
			return timings.HelperVerificationMicros > 0 && timings.LaunchServicesStartMicros > 0
		})
	})

	t.Run("typed timeout", func(t *testing.T) {
		launchPhotoKitCurrentStillApp = func(_ context.Context, _, _, responsePath string) error {
			data, err := proto.Marshal(&fetchwire.CurrentStillFetchResponse{FailureKind: "timeout", ErrorMessage: "PhotoKit current-still request timed out", HelperStartedUnixNanos: time.Now().UnixNano(), PhotokitCallbackMicros: 9, PhotokitCalls: 1})
			if err != nil {
				return err
			}
			return os.WriteFile(responsePath, data, 0o600)
		}
		_, err := ExportCurrentStillThroughApp(context.Background(), request, filepath.Join(t.TempDir(), "current.heic"))
		if !errors.Is(err, ErrPhotoKitExportTimedOut) {
			t.Fatalf("error = %v", err)
		}
		assertMeasuredFactFailure(t, err, 1, func(timings CurrentStillPhaseTimings) bool {
			return timings.HelperVerificationMicros > 0 && timings.LaunchServicesStartMicros > 0 && timings.PhotoKitCallbackMicros == 9
		})
	})

	t.Run("cancelled after response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		launchPhotoKitCurrentStillApp = func(_ context.Context, _, _, responsePath string) error {
			data, err := proto.Marshal(&fetchwire.CurrentStillFetchResponse{Success: true, HelperStartedUnixNanos: time.Now().UnixNano(), PhotokitCalls: 1})
			if err != nil {
				return err
			}
			if err := os.WriteFile(responsePath, data, 0o600); err != nil {
				return err
			}
			cancel()
			return nil
		}
		_, err := ExportCurrentStillThroughApp(ctx, request, filepath.Join(t.TempDir(), "current.heic"))
		assertMeasuredFactFailure(t, err, 1, func(timings CurrentStillPhaseTimings) bool {
			return timings.HelperVerificationMicros > 0 && timings.LaunchServicesStartMicros > 0
		})
	})

	t.Run("inspect output", func(t *testing.T) {
		launchPhotoKitCurrentStillApp = func(_ context.Context, _, _, responsePath string) error {
			data, err := proto.Marshal(&fetchwire.CurrentStillFetchResponse{Success: true, HelperStartedUnixNanos: time.Now().UnixNano(), PhotokitCalls: 1})
			if err != nil {
				return err
			}
			return os.WriteFile(responsePath, data, 0o600)
		}
		_, err := ExportCurrentStillThroughApp(context.Background(), request, filepath.Join(t.TempDir(), "current.heic"))
		assertMeasuredFactFailure(t, err, 1, func(timings CurrentStillPhaseTimings) bool {
			return timings.HelperVerificationMicros > 0 && timings.LaunchServicesStartMicros > 0 && timings.ValidationHashMicros > 0
		})
	})

	t.Run("mismatched proof", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "current.heic")
		launchPhotoKitCurrentStillApp = func(_ context.Context, _, _, responsePath string) error {
			if err := os.WriteFile(destination, []byte("synthetic current bytes"), 0o600); err != nil {
				return err
			}
			data, err := proto.Marshal(&fetchwire.CurrentStillFetchResponse{Success: true, SizeBytes: 1, Sha256: make([]byte, sha256.Size), MediaType: "public.heic", PixelWidth: 2, PixelHeight: 3, HelperStartedUnixNanos: time.Now().UnixNano(), PhotokitCalls: 1})
			if err != nil {
				return err
			}
			return os.WriteFile(responsePath, data, 0o600)
		}
		_, err := ExportCurrentStillThroughApp(context.Background(), request, destination)
		assertMeasuredFactFailure(t, err, 1, func(timings CurrentStillPhaseTimings) bool {
			return timings.HelperVerificationMicros > 0 && timings.LaunchServicesStartMicros > 0 && timings.ValidationHashMicros > 0
		})
	})
}

func assertMeasuredFactFailure(t *testing.T, err error, calls int, valid func(CurrentStillPhaseTimings) bool) {
	t.Helper()
	var measured *CurrentStillMeasuredError
	if !errors.As(err, &measured) || measured.PhotoKitCalls != calls || !valid(measured.Timings) {
		t.Fatalf("measured failure = %#v, error = %v", measured, err)
	}
}

func TestPhotoKitFetchLaunchUsesVerifiedAppPath(t *testing.T) {
	oldResolve := resolvePhotoKitFetchApp
	oldRun := runPhotoKitFetchOpen
	defer func() {
		resolvePhotoKitFetchApp = oldResolve
		runPhotoKitFetchOpen = oldRun
	}()

	const appPath = "/synthetic/Applications/Photoscrawl Fetch.app"
	resolvePhotoKitFetchApp = func(context.Context) (string, error) {
		return appPath, nil
	}
	var gotApp, gotRequest, gotResponse string
	runPhotoKitFetchOpen = func(_ context.Context, app, request, response string) error {
		gotApp, gotRequest, gotResponse = app, request, response
		return nil
	}
	if err := launchPhotoKitFetchApp(context.Background(), "request.pb", "response.pb"); err != nil {
		t.Fatal(err)
	}
	if gotApp != appPath || gotRequest != "request.pb" || gotResponse != "response.pb" {
		t.Fatalf("launch target = %q %q %q", gotApp, gotRequest, gotResponse)
	}
	wantArgs := []string{"-n", "-g", appPath, "--args", "run", "--request", "request.pb", "--response", "response.pb"}
	if got := photoKitFetchOpenArgs(appPath, "request.pb", "response.pb"); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("open args = %#v, want %#v", got, wantArgs)
	} else {
		t.Logf("boundary=launch_argument_vector raw_argv=%q", got)
	}
}

func TestPhotoLibraryAccessStatusUsesTheVerifiedHelper(t *testing.T) {
	oldResolve, oldLaunch := resolvePhotoKitFetchApp, launchPhotoKitPermissionApp
	t.Cleanup(func() {
		resolvePhotoKitFetchApp = oldResolve
		launchPhotoKitPermissionApp = oldLaunch
	})
	resolvePhotoKitFetchApp = func(context.Context) (string, error) {
		return "/synthetic/Photoscrawl Fetch.app", nil
	}
	called := 0
	launchPhotoKitPermissionApp = func(_ context.Context, appPath, operation, responsePath string) error {
		called++
		if appPath != "/synthetic/Photoscrawl Fetch.app" || operation != "status" {
			t.Fatalf("launch = %q %q", appPath, operation)
		}
		data, err := proto.Marshal(&fetchwire.OriginalFetchResponse{Success: true, PhotosAccessStatus: "not_determined"})
		if err != nil {
			return err
		}
		return os.WriteFile(responsePath, data, 0o600)
	}
	status, err := PhotoLibraryAccessStatusThroughApp(context.Background(), false)
	if err != nil || status != "not_determined" || called != 1 {
		t.Fatalf("status=%q err=%v called=%d", status, err, called)
	}
	want := []string{"-g", "-n", "/synthetic/Photoscrawl Fetch.app", "--args", "permission", "status", "--response", "response.pb"}
	if got := photoKitFetchPermissionOpenArgs("/synthetic/Photoscrawl Fetch.app", "status", "response.pb"); !reflect.DeepEqual(got, want) {
		t.Fatalf("open args = %#v, want %#v", got, want)
	}
	want = []string{"-n", "/synthetic/Photoscrawl Fetch.app", "--args", "permission", "request", "--response", "response.pb"}
	if got := photoKitFetchPermissionOpenArgs("/synthetic/Photoscrawl Fetch.app", "request", "response.pb"); !reflect.DeepEqual(got, want) {
		t.Fatalf("request open args = %#v, want %#v", got, want)
	}
}

func TestPhotoLibraryAccessStatusWaitsForDetachedHelperResponse(t *testing.T) {
	oldResolve, oldLaunch := resolvePhotoKitFetchApp, launchPhotoKitPermissionApp
	t.Cleanup(func() {
		resolvePhotoKitFetchApp = oldResolve
		launchPhotoKitPermissionApp = oldLaunch
	})
	resolvePhotoKitFetchApp = func(context.Context) (string, error) { return "/synthetic/Photoscrawl Fetch.app", nil }
	launchPhotoKitPermissionApp = func(_ context.Context, _, _, responsePath string) error {
		go func() {
			time.Sleep(25 * time.Millisecond)
			data, err := proto.Marshal(&fetchwire.OriginalFetchResponse{Success: true, PhotosAccessStatus: "authorized"})
			if err == nil {
				_ = os.WriteFile(responsePath, data, 0o600)
			}
		}()
		return nil
	}
	status, err := PhotoLibraryAccessStatusThroughApp(context.Background(), true)
	if err != nil || status != "authorized" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestPhotoKitFetchLaunchStopsBeforeOpenWhenVerificationFails(t *testing.T) {
	oldResolve := resolvePhotoKitFetchApp
	oldRun := runPhotoKitFetchOpen
	defer func() {
		resolvePhotoKitFetchApp = oldResolve
		runPhotoKitFetchOpen = oldRun
	}()

	resolvePhotoKitFetchApp = func(context.Context) (string, error) {
		return "", errors.New("synthetic signature mismatch")
	}
	opened := false
	runPhotoKitFetchOpen = func(context.Context, string, string, string) error {
		opened = true
		return nil
	}
	err := launchPhotoKitFetchApp(context.Background(), "request.pb", "response.pb")
	if err == nil || err.Error() != "synthetic signature mismatch" {
		t.Fatalf("launch error = %v", err)
	}
	if opened {
		t.Fatal("LaunchServices was invoked after helper verification failed")
	}
}

func TestExportOriginalResourceThroughAppReturnsTypedAccessFailure(t *testing.T) {
	oldLaunch := launchPhotoKitFetchApp
	launchPhotoKitFetchApp = func(_ context.Context, _, responsePath string) error {
		data, err := proto.Marshal(&fetchwire.OriginalFetchResponse{
			FailureKind:        "photos_access",
			ErrorMessage:       "Photos access is denied",
			PhotosAccessStatus: "denied",
		})
		if err != nil {
			t.Fatal(err)
		}
		return os.WriteFile(responsePath, data, 0o600)
	}
	defer func() { launchPhotoKitFetchApp = oldLaunch }()

	err := ExportOriginalResourceThroughApp(context.Background(), OriginalExportQuery{LocalIdentifier: "synthetic-asset"}, filepath.Join(t.TempDir(), "original.heic"), false)
	var accessErr *PhotoLibraryAccessError
	if !errors.As(err, &accessErr) || accessErr.Status != "denied" {
		t.Fatalf("error = %T %v", err, err)
	}
}

func TestPhotoKitAppFailurePreservesTypedExportError(t *testing.T) {
	err := photoKitAppFailure(&fetchwire.OriginalFetchResponse{
		FailureKind:  "photokit_export",
		ErrorMessage: "PhotoKit could not export the selected camera original",
		ErrorDomain:  "PHPhotosErrorDomain",
		ErrorCode:    3303,
	})
	var exportErr *PhotoKitExportError
	if !errors.As(err, &exportErr) || exportErr.Domain != "PHPhotosErrorDomain" || exportErr.Code != 3303 {
		t.Fatalf("error = %T %#v", err, err)
	}
}

func TestExportOriginalResourceThroughAppDeletesMismatchedOutput(t *testing.T) {
	oldLaunch := launchPhotoKitFetchApp
	launchPhotoKitFetchApp = func(_ context.Context, requestPath, responsePath string) error {
		requestData, err := os.ReadFile(requestPath)
		if err != nil {
			t.Fatal(err)
		}
		var request fetchwire.OriginalFetchRequest
		if err := proto.Unmarshal(requestData, &request); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(request.DestinationPath, []byte("wrong bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(&fetchwire.OriginalFetchResponse{Success: true, SizeBytes: 99, Sha256: make([]byte, sha256.Size)})
		if err != nil {
			t.Fatal(err)
		}
		return os.WriteFile(responsePath, data, 0o600)
	}
	defer func() { launchPhotoKitFetchApp = oldLaunch }()

	destination := filepath.Join(t.TempDir(), "original.heic")
	err := ExportOriginalResourceThroughApp(context.Background(), OriginalExportQuery{LocalIdentifier: "synthetic-asset"}, destination, false)
	if err == nil {
		t.Fatal("mismatched output accepted")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched output remains: %v", statErr)
	}
}

func TestExportOriginalResourceThroughAppWaitsForDetachedAppAfterCancellation(t *testing.T) {
	oldLaunch := launchPhotoKitFetchApp
	started := make(chan struct{})
	finish := make(chan struct{})
	launchPhotoKitFetchApp = func(launchCtx context.Context, requestPath, _ string) error {
		requestData, err := os.ReadFile(requestPath)
		if err != nil {
			return err
		}
		var request fetchwire.OriginalFetchRequest
		if err := proto.Unmarshal(requestData, &request); err != nil {
			return err
		}
		close(started)
		select {
		case <-launchCtx.Done():
			return fmt.Errorf("signed app lifecycle ended before its request: %w", launchCtx.Err())
		case <-finish:
		}
		return os.WriteFile(request.DestinationPath, []byte("late detached output"), 0o600)
	}
	defer func() { launchPhotoKitFetchApp = oldLaunch }()

	root := t.TempDir()
	destination := filepath.Join(root, "original.heic")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- ExportOriginalResourceThroughApp(ctx, OriginalExportQuery{LocalIdentifier: "synthetic-asset"}, destination, false)
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		t.Fatalf("export returned before the detached app finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("export error = %v, want context canceled", err)
	}
	for _, path := range []string{destination, destination + ".exporting"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cancelled export left output %q: %v", filepath.Base(path), err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, ".photokit-request-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("cancelled export left %d wire directories", len(matches))
	}
}
