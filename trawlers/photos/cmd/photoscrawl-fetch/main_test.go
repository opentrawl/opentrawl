//go:build darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos/fetchwire"
	"google.golang.org/protobuf/proto"
)

func TestPermissionStatusIsPassive(t *testing.T) {
	oldStatus, oldRequest := photoLibraryAuthorizationStatus, requestAuthorization
	t.Cleanup(func() {
		photoLibraryAuthorizationStatus = oldStatus
		requestAuthorization = oldRequest
	})
	photoLibraryAuthorizationStatus = func(context.Context) (string, error) { return "not_determined", nil }
	requestAuthorization = func(context.Context) (string, error) {
		t.Fatal("passive status requested Photos access")
		return "", nil
	}
	response := permissionResponse(t, "status")
	if !response.Success || response.PhotosAccessStatus != "not_determined" {
		t.Fatalf("response = %#v", response)
	}
}

func TestPermissionRequestOnlyPromptsWhenUndecided(t *testing.T) {
	oldStatus, oldRequest := photoLibraryAuthorizationStatus, requestAuthorization
	t.Cleanup(func() {
		photoLibraryAuthorizationStatus = oldStatus
		requestAuthorization = oldRequest
	})
	for _, test := range []struct {
		name, before, after string
		requests            int
	}{
		{name: "undecided", before: "not_determined", after: "authorized", requests: 1},
		{name: "denied", before: "denied", after: "", requests: 0},
		{name: "limited", before: "limited", after: "", requests: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			photoLibraryAuthorizationStatus = func(context.Context) (string, error) { return test.before, nil }
			requestAuthorization = func(context.Context) (string, error) {
				requests++
				return test.after, nil
			}
			response := permissionResponse(t, "request")
			want := test.before
			if test.after != "" {
				want = test.after
			}
			if !response.Success || response.PhotosAccessStatus != want || requests != test.requests {
				t.Fatalf("response=%#v requests=%d", response, requests)
			}
		})
	}
}

func TestPermissionRejectsUnknownNativeStatus(t *testing.T) {
	oldStatus, oldRequest := photoLibraryAuthorizationStatus, requestAuthorization
	t.Cleanup(func() {
		photoLibraryAuthorizationStatus = oldStatus
		requestAuthorization = oldRequest
	})
	photoLibraryAuthorizationStatus = func(context.Context) (string, error) { return "unknown", nil }
	requestAuthorization = func(context.Context) (string, error) {
		t.Fatal("unknown status requested Photos access")
		return "", nil
	}
	response := permissionResponse(t, "request")
	if response.Success || response.FailureKind != "native_status" {
		t.Fatalf("response = %#v", response)
	}
}

func TestPermissionRequestWritesTypedFailureWhenNativeRequestTimesOut(t *testing.T) {
	oldStatus, oldRequest := photoLibraryAuthorizationStatus, requestAuthorization
	t.Cleanup(func() {
		photoLibraryAuthorizationStatus = oldStatus
		requestAuthorization = oldRequest
	})
	photoLibraryAuthorizationStatus = func(context.Context) (string, error) { return "not_determined", nil }
	requestAuthorization = func(context.Context) (string, error) {
		return "", errors.New("PhotoKit authorization request timed out")
	}
	response := permissionResponse(t, "request")
	if response.Success || response.FailureKind != "native_status" || response.ErrorMessage != "PhotoKit could not read Photos access" {
		t.Fatalf("response = %#v", response)
	}
}

func permissionResponse(t *testing.T, operation string) *fetchwire.OriginalFetchResponse {
	t.Helper()
	responsePath := filepath.Join(t.TempDir(), "response.pb")
	if code := run(context.Background(), []string{"permission", operation, "--response", responsePath}, io.Discard); code != 0 && code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	data, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	response := &fetchwire.OriginalFetchResponse{}
	if err := proto.Unmarshal(data, response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestDirectCommandsAreRejected(t *testing.T) {
	var stderr bytes.Buffer
	if exitCode := run(context.Background(), []string{"export"}, &stderr); exitCode != 2 {
		t.Fatalf("exit code = %d stderr = %q", exitCode, stderr.String())
	}
}

func TestRunReadinessWireRequestReturnsIdentityAndResourceFacts(t *testing.T) {
	oldReadiness := assetReadinessForUUID
	t.Cleanup(func() { assetReadinessForUUID = oldReadiness })
	assetReadinessForUUID = func(_ context.Context, assetUUID string) (photos.AssetReadiness, error) {
		if assetUUID != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE" {
			t.Fatalf("asset UUID = %q", assetUUID)
		}
		return photos.AssetReadiness{
			LocalIdentifier:  "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001",
			AssetUUID:        "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
			MediaType:        "image",
			CreationDate:     "2026-07-14T12:00:00Z",
			ModificationDate: "2026-07-14T12:00:01Z",
			PixelWidth:       4032,
			PixelHeight:      3024,
			OriginalFilename: "synthetic.heic",
			OriginalUTI:      "public.heic",
		}, nil
	}
	requestPath := filepath.Join(t.TempDir(), "request.pb")
	responsePath := filepath.Join(t.TempDir(), "response.pb")
	request, err := proto.Marshal(&fetchwire.AssetReadinessRequest{AssetUuid: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"run-readiness", "--request", requestPath, "--response", responsePath}, io.Discard); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	data, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	var response fetchwire.AssetReadinessResponse
	if err := proto.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.AssetUuid != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE" || response.LocalIdentifier == "" || response.OriginalFilename != "synthetic.heic" || response.HasLocation {
		t.Fatalf("response = %#v", response)
	}
}

func TestRunWireRequestExportsExactOriginalAndProof(t *testing.T) {
	const capturedRequestHex = "0a0f73796e7468657469632d61737365742a0e73796e7468657469632e68656963320d6f726967696e616c2e68656963380140c0a907"
	const capturedResponseHex = "0801101e1a2046aaee4914ea18f1c75caf43585122c198f09291000416caa8aa743ce102ab72"
	const capturedDestination = "original.heic"
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)
	requestData, err := hex.DecodeString(capturedRequestHex)
	if err != nil {
		t.Fatal(err)
	}
	wantResponse, err := hex.DecodeString(capturedResponseHex)
	if err != nil {
		t.Fatal(err)
	}
	var capturedRequest fetchwire.OriginalFetchRequest
	if err := proto.Unmarshal(requestData, &capturedRequest); err != nil {
		t.Fatal(err)
	}
	if capturedRequest.DestinationPath != capturedDestination {
		t.Fatalf("captured destination = %q, want %q", capturedRequest.DestinationPath, capturedDestination)
	}
	oldExport := exportOriginalMatching
	exportOriginalMatching = func(_ context.Context, query photos.OriginalExportQuery, destination string, allowNetwork bool) error {
		if query.LocalIdentifier != "synthetic-asset" || query.OriginalFilename != "synthetic.heic" || !allowNetwork {
			t.Fatalf("query = %#v allow network = %t", query, allowNetwork)
		}
		return os.WriteFile(destination, []byte("exact synthetic original bytes"), 0o600)
	}
	defer func() { exportOriginalMatching = oldExport }()

	requestPath := filepath.Join(workingDirectory, "request.pb")
	responsePath := filepath.Join(workingDirectory, "response.pb")
	destination := filepath.Join(workingDirectory, capturedDestination)
	t.Logf("boundary=synthetic_original_request raw_hex=%x", requestData)
	if err := os.WriteFile(requestPath, requestData, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if exitCode := run(context.Background(), []string{"run", "--request", requestPath, "--response", responsePath}, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d stderr = %q", exitCode, stderr.String())
	}
	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=synthetic_original_response raw_hex=%x", responseData)
	if !bytes.Equal(responseData, wantResponse) {
		t.Fatalf("raw response = %x, want captured %x", responseData, wantResponse)
	}
	var response fetchwire.OriginalFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("exact synthetic original bytes"))
	if !response.Success || response.SizeBytes != 30 || !bytes.Equal(response.Sha256, digest[:]) {
		t.Fatalf("response = %#v", response)
	}
	media, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=synthetic_original_media raw_bytes=%q", media)
}

func TestRunWireRequestReturnsTypedPhotosAccessFailure(t *testing.T) {
	oldExport := exportOriginalMatching
	exportOriginalMatching = func(context.Context, photos.OriginalExportQuery, string, bool) error {
		return &photos.PhotoLibraryAccessError{Status: "denied"}
	}
	defer func() { exportOriginalMatching = oldExport }()

	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.pb")
	responsePath := filepath.Join(dir, "response.pb")
	requestData, err := proto.Marshal(&fetchwire.OriginalFetchRequest{
		LocalIdentifier:     "synthetic-asset",
		DestinationPath:     filepath.Join(dir, "original.heic"),
		TimeoutMilliseconds: time.Minute.Milliseconds(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, requestData, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if exitCode := run(context.Background(), []string{"run", "--request", requestPath, "--response", responsePath}, &stderr); exitCode != 1 {
		t.Fatalf("exit code = %d stderr = %q", exitCode, stderr.String())
	}
	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	var response fetchwire.OriginalFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		t.Fatal(err)
	}
	if response.FailureKind != "photos_access" || response.PhotosAccessStatus != "denied" {
		t.Fatalf("response = %#v", response)
	}
}

func TestWireErrorResponsePreservesTypedPhotoKitFailure(t *testing.T) {
	response := wireErrorResponse(&photos.PhotoKitExportError{Domain: "PHPhotosErrorDomain", Code: 3303})
	if response.FailureKind != "photokit_export" || response.ErrorDomain != "PHPhotosErrorDomain" || response.ErrorCode != 3303 {
		t.Fatalf("response = %#v", response)
	}
	if response.ErrorMessage == "" {
		t.Fatal("typed PhotoKit failure lost its safe message")
	}
}

func TestCurrentStillWireErrorResponsePreservesCallbackFacts(t *testing.T) {
	response := currentStillWireErrorResponse(photos.NewPhotoKitCallbackError("PHPhotosErrorDomain", 3303, "callback /private/source", true, true, false, true))
	if response.FailureKind != "photokit_export" || response.ErrorDomain != "PHPhotosErrorDomain" || response.ErrorCode != 3303 {
		t.Fatalf("response = %#v", response)
	}
	if strings.Contains(response.ErrorMessage, "/private/source") {
		t.Fatalf("response leaked callback detail: %q", response.ErrorMessage)
	}
	for _, want := range []string{"cancelled=true", "degraded=true", "in_cloud=false", "callback_returned=true"} {
		if !strings.Contains(response.ErrorMessage, want) {
			t.Fatalf("response message = %q, missing %q", response.ErrorMessage, want)
		}
	}
}

func TestCurrentStillWireErrorResponsePreservesTimeoutFacts(t *testing.T) {
	err := photos.NewPhotoKitCallbackTimeoutError("", 0, "photokit original export timed out", false, true, true)
	if !errors.Is(err, photos.ErrPhotoKitExportTimedOut) {
		t.Fatalf("error = %v, want timeout", err)
	}
	response := currentStillWireErrorResponse(err)
	if response.FailureKind != "timeout" {
		t.Fatalf("response = %#v", response)
	}
	for _, want := range []string{"degraded=true", "in_cloud=true", "timed_out=true"} {
		if !strings.Contains(response.ErrorMessage, want) {
			t.Fatalf("response message = %q, missing %q", response.ErrorMessage, want)
		}
	}
}

func TestCurrentStillWireErrorResponseUsesFixedStageTokensWithoutLeakage(t *testing.T) {
	for _, stage := range []string{
		photos.CurrentStillStageSelectionValidation,
		photos.CurrentStillStageImageDecode,
		photos.CurrentStillStageImageDimensions,
		photos.CurrentStillStageOutputWrite,
		photos.CurrentStillStagePrepareDestination,
		photos.CurrentStillStageRenameOutput,
		photos.CurrentStillStageInspectOutput,
	} {
		err := photos.NewCurrentStillStageError(stage, errors.New("synthetic failure at /private/runtime/asset-uuid"))
		response := currentStillWireErrorResponse(err)
		if response.FailureKind != "export_failed" {
			t.Fatalf("stage=%q response=%#v", stage, response)
		}
		want := fmt.Sprintf("PhotoKit current-still request failed (stage=%s)", stage)
		if response.ErrorMessage != want {
			t.Fatalf("stage=%q message=%q want=%q", stage, response.ErrorMessage, want)
		}
		if strings.Contains(response.ErrorMessage, "/private/runtime") || strings.Contains(response.ErrorMessage, "asset-uuid") {
			t.Fatalf("stage=%q response leaked input: %q", stage, response.ErrorMessage)
		}
	}
}

func TestRunCurrentStillAppKeepsMainLoopAliveUntilResponseWorkFinishes(t *testing.T) {
	originalPrepare := prepareCurrentMainLoop
	originalRun := runCurrentMainLoop
	originalStop := stopCurrentMainLoop
	t.Cleanup(func() {
		prepareCurrentMainLoop = originalPrepare
		runCurrentMainLoop = originalRun
		stopCurrentMainLoop = originalStop
	})
	prepared := make(chan struct{})
	stopped := make(chan struct{})
	prepareCurrentMainLoop = func() bool { close(prepared); return true }
	runCurrentMainLoop = func() {
		<-prepared
		<-stopped
	}
	stopCurrentMainLoop = func() { close(stopped) }
	if code := runCurrentStillApp([]string{"run-current-still"}, io.Discard); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRunCurrentStillWireRequestPreservesExplicitNetworkAndFacts(t *testing.T) {
	oldExport := exportCurrentStill
	defer func() { exportCurrentStill = oldExport }()
	exportCurrentStill = func(_ context.Context, request photos.CurrentStillNativeRequest, destination string) (photos.CurrentStillFact, error) {
		if request.AssetUUID != "synthetic-asset" || !request.HasExpectedModification || request.Modification.UnixSeconds != 1783771200 || request.Modification.Microseconds != 125000 || request.AllowNetwork {
			t.Fatalf("request = %#v", request)
		}
		data := []byte("exact synthetic current-still bytes")
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		return photos.CurrentStillFact{MediaType: "public.heic", Orientation: 1, PixelWidth: 4032, PixelHeight: 3024, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Timings: photos.CurrentStillPhaseTimings{PhotoKitCallbackMicros: 31, ValidationHashMicros: 32}, PhotoKitCalls: 1}, nil
	}
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.pb")
	responsePath := filepath.Join(dir, "response.pb")
	destination := filepath.Join(dir, "current.heic")
	data, err := proto.Marshal(&fetchwire.CurrentStillFetchRequest{SourceLibraryId: "synthetic-library", AssetUuid: "synthetic-asset", ModificationUnixSeconds: 1783771200, ModificationMicroseconds: 125000, HasExpectedModification: true, DestinationPath: destination, TimeoutMilliseconds: time.Minute.Milliseconds()})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=synthetic_current_still_request raw_hex=%x", data)
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"run-current-still", "--request", requestPath, "--response", responsePath}, &stderr); code != 0 {
		t.Fatalf("exit = %d stderr=%q", code, stderr.String())
	}
	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=synthetic_current_still_response raw_hex=%x", responseData)
	var response fetchwire.CurrentStillFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.MediaType != "public.heic" || response.PixelWidth != 4032 || response.PixelHeight != 3024 || response.HelperStartedUnixNanos <= 0 || response.PhotokitCallbackMicros != 31 || response.ValidationHashMicros != 32 || response.PhotokitCalls != 1 {
		t.Fatalf("response = %#v", response)
	}
}

func TestRunCurrentStillWireRequestOmitsExpectedModification(t *testing.T) {
	oldExport := exportCurrentStill
	defer func() { exportCurrentStill = oldExport }()
	exportCurrentStill = func(_ context.Context, request photos.CurrentStillNativeRequest, destination string) (photos.CurrentStillFact, error) {
		if request.HasExpectedModification || request.Modification != (photos.CurrentStillModification{}) {
			t.Fatalf("native request fabricated a modification instant: %#v", request)
		}
		data := []byte("exact synthetic current-still bytes")
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		return photos.CurrentStillFact{MediaType: "public.heic", Orientation: 1, PixelWidth: 4032, PixelHeight: 3024, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
	}
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.pb")
	responsePath := filepath.Join(dir, "response.pb")
	destination := filepath.Join(dir, "current.heic")
	data := mustMarshalCurrentStillRequest(t, &fetchwire.CurrentStillFetchRequest{SourceLibraryId: "synthetic-library", AssetUuid: "synthetic-asset", DestinationPath: destination, TimeoutMilliseconds: time.Minute.Milliseconds()})
	t.Logf("boundary=synthetic_current_still_without_modification raw_hex=%x", data)
	var request fetchwire.CurrentStillFetchRequest
	if err := proto.Unmarshal(data, &request); err != nil || request.HasExpectedModification || request.ModificationUnixSeconds != 0 || request.ModificationMicroseconds != 0 {
		t.Fatalf("wire request = %#v, %v", request, err)
	}
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"run-current-still", "--request", requestPath, "--response", responsePath}, io.Discard); code != 0 {
		t.Fatalf("exit = %d", code)
	}
}

func TestApplyCurrentStillErrorTimingsPreservesFailedObservation(t *testing.T) {
	response := &fetchwire.CurrentStillFetchResponse{}
	err := &photos.CurrentStillMeasuredError{Cause: errors.New("synthetic failure"), Timings: photos.CurrentStillPhaseTimings{PhotoKitCallbackMicros: 41, ValidationHashMicros: 42}, PhotoKitCalls: 1}
	applyCurrentStillErrorTimings(response, err)
	if response.PhotokitCallbackMicros != 41 || response.ValidationHashMicros != 42 || response.PhotokitCalls != 1 {
		t.Fatalf("response = %#v", response)
	}
}

func TestRunCurrentStillWireRequestPreservesStartAndZeroCallsOnEarlyFailures(t *testing.T) {
	tests := []struct {
		name    string
		request []byte
		write   bool
	}{
		{name: "read", write: false},
		{name: "decode", request: []byte{0xff}, write: true},
		{name: "unexpected modification", request: mustMarshalCurrentStillRequest(t, &fetchwire.CurrentStillFetchRequest{SourceLibraryId: "synthetic-library", AssetUuid: "synthetic-asset", ModificationUnixSeconds: 1783771200, ModificationMicroseconds: 125000, DestinationPath: filepath.Join(t.TempDir(), "current.heic"), TimeoutMilliseconds: time.Minute.Milliseconds()}), write: true},
		{name: "missing expected modification", request: mustMarshalCurrentStillRequest(t, &fetchwire.CurrentStillFetchRequest{SourceLibraryId: "synthetic-library", AssetUuid: "synthetic-asset", HasExpectedModification: true, DestinationPath: filepath.Join(t.TempDir(), "current.heic"), TimeoutMilliseconds: time.Minute.Milliseconds()}), write: true},
		{name: "timeout validation", request: mustMarshalCurrentStillRequest(t, &fetchwire.CurrentStillFetchRequest{SourceLibraryId: "synthetic-library", AssetUuid: "synthetic-asset", DestinationPath: filepath.Join(t.TempDir(), "current.heic")}), write: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			requestPath := filepath.Join(dir, "request.pb")
			responsePath := filepath.Join(dir, "response.pb")
			if test.write {
				if err := os.WriteFile(requestPath, test.request, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if code := runCurrentStillWireRequest(context.Background(), []string{"--request", requestPath, "--response", responsePath}, io.Discard); code != 1 {
				t.Fatalf("exit = %d", code)
			}
			data, err := os.ReadFile(responsePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("boundary=synthetic_early_failure_%s raw_response_hex=%x", test.name, data)
			var response fetchwire.CurrentStillFetchResponse
			if err := proto.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
			if response.FailureKind != "invalid_request" || response.HelperStartedUnixNanos <= 0 || response.PhotokitCalls != 0 {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func mustMarshalCurrentStillRequest(t *testing.T, request *fetchwire.CurrentStillFetchRequest) []byte {
	t.Helper()
	data, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
