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

func TestExportCurrentStillThroughAppStopsBeforeLaunchAfterCancellation(t *testing.T) {
	oldLaunch := launchPhotoKitCurrentStillApp
	defer func() { launchPhotoKitCurrentStillApp = oldLaunch }()
	launched := false
	launchPhotoKitCurrentStillApp = func(context.Context, string, string) error { launched = true; return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ExportCurrentStillThroughApp(ctx, CurrentStillRequest{SourceLibraryID: "synthetic-library", AssetUUID: "synthetic-asset", Modification: CurrentStillModification{UnixSeconds: 1783771200, Microseconds: 125000}}, filepath.Join(t.TempDir(), "current.heic"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if launched {
		t.Fatal("cancelled current-still request launched the helper")
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
	wantArgs := []string{"-W", "-n", "-g", appPath, "--args", "run", "--request", "request.pb", "--response", "response.pb"}
	if got := photoKitFetchOpenArgs(appPath, "request.pb", "response.pb"); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("open args = %#v, want %#v", got, wantArgs)
	} else {
		t.Logf("boundary=launch_argument_vector raw_argv=%q", got)
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
