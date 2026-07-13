//go:build darwin

// Command photoscrawl-fetch is the signed internal PhotoKit boundary for one
// original export. It is not part of the trawl command surface.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos/fetchwire"
	"google.golang.org/protobuf/proto"
)

var (
	photoLibraryAuthorizationStatus = photos.PhotoLibraryAuthorizationStatus
	requestAuthorization            = photos.RequestPhotoLibraryAuthorization
	exportOriginalMatching          = photos.ExportOriginalResourceMatching
	exportCurrentStill              = photos.ExportCurrentStillMatching
	assetReadinessForUUID           = photos.AssetReadinessForUUID
	prepareCurrentMainLoop          = photos.PrepareCurrentStillMainLoop
	runCurrentMainLoop              = photos.RunCurrentStillMainLoop
	stopCurrentMainLoop             = photos.StopCurrentStillMainLoop
)

func init() { runtime.LockOSThread() }

func main() {
	if len(os.Args) == 6 && os.Args[1] == "run-current-still" {
		os.Exit(runCurrentStillApp(os.Args[1:], os.Stderr))
	}
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr))
}

func runCurrentStillApp(args []string, stderr io.Writer) int {
	if !prepareCurrentMainLoop() {
		writeln(stderr, "photoscrawl-fetch current-still main loop must run on the native main thread")
		return 1
	}
	result := make(chan int, 1)
	go func() {
		code := run(context.Background(), args, stderr)
		result <- code
		stopCurrentMainLoop()
	}()
	runCurrentMainLoop()
	return <-result
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 5 && args[0] == "run" {
		return runWireRequest(ctx, args[1:], stderr)
	}
	if len(args) == 5 && args[0] == "run-current-still" {
		return runCurrentStillWireRequest(ctx, args[1:], stderr)
	}
	if len(args) == 5 && args[0] == "run-readiness" {
		return runReadinessWireRequest(ctx, args[1:], stderr)
	}
	if len(args) == 4 && args[0] == "permission" {
		return runPermission(ctx, args[1:], stderr)
	}
	{
		writeln(stderr, "photoscrawl-fetch is an internal app and accepts no direct commands")
		return 2
	}
}

func runReadinessWireRequest(ctx context.Context, args []string, stderr io.Writer) int {
	requestPath, responsePath, ok := wirePaths(args, stderr)
	if !ok {
		return 2
	}
	data, err := readWireRequest(requestPath)
	if err != nil {
		_ = writeReadinessWireResponse(responsePath, failedReadinessResponse("invalid_request", "PhotoKit asset readiness request could not be read", nil))
		return 1
	}
	var request fetchwire.AssetReadinessRequest
	if err := proto.Unmarshal(data, &request); err != nil || request.AssetUuid == "" {
		_ = writeReadinessWireResponse(responsePath, failedReadinessResponse("invalid_request", "PhotoKit asset readiness request is invalid", nil))
		return 1
	}
	readiness, err := assetReadinessForUUID(ctx, request.AssetUuid)
	if err != nil {
		kind := "readiness_failed"
		_ = writeReadinessWireResponse(responsePath, failedReadinessResponse(kind, "PhotoKit could not read asset readiness", err))
		return 1
	}
	if err := writeReadinessWireResponse(responsePath, &fetchwire.AssetReadinessResponse{
		Success:          true,
		LocalIdentifier:  readiness.LocalIdentifier,
		AssetUuid:        readiness.AssetUUID,
		MediaType:        readiness.MediaType,
		HasLocation:      readiness.HasLocation,
		CreationDate:     readiness.CreationDate,
		ModificationDate: readiness.ModificationDate,
		PixelWidth:       readiness.PixelWidth,
		PixelHeight:      readiness.PixelHeight,
		OriginalFilename: readiness.OriginalFilename,
		OriginalUti:      readiness.OriginalUTI,
	}); err != nil {
		return 1
	}
	return 0
}

func failedReadinessResponse(kind, message string, err error) *fetchwire.AssetReadinessResponse {
	response := &fetchwire.AssetReadinessResponse{FailureKind: kind, ErrorMessage: message}
	var accessErr *photos.PhotoLibraryAccessError
	if errors.As(err, &accessErr) {
		response.FailureKind = "photos_access"
		response.PhotosAccessStatus = accessErr.Status
	}
	return response
}

func writeReadinessWireResponse(path string, response *fetchwire.AssetReadinessResponse) error {
	data, err := proto.Marshal(response)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func runPermission(ctx context.Context, args []string, stderr io.Writer) int {
	operation := args[0]
	flags := flag.NewFlagSet("permission", flag.ContinueOnError)
	flags.SetOutput(stderr)
	responsePath := flags.String("response", "", "protobuf response path")
	if (operation != "status" && operation != "request") || flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *responsePath == "" {
		writeln(stderr, "photoscrawl-fetch permission: status or request and --response are required")
		return 2
	}
	status, err := photoLibraryAuthorizationStatus(ctx)
	if err == nil && operation == "request" && status == "not_determined" {
		status, err = requestAuthorization(ctx)
	}
	if err != nil {
		_ = writeWireResponse(*responsePath, failedWireResponse("native_status", "PhotoKit could not read Photos access", nil))
		return 1
	}
	if !validPhotosAccessStatus(status) {
		_ = writeWireResponse(*responsePath, failedWireResponse("native_status", "PhotoKit returned an unrecognised Photos access state", nil))
		return 1
	}
	if err := writeWireResponse(*responsePath, &fetchwire.OriginalFetchResponse{Success: true, PhotosAccessStatus: status}); err != nil {
		return 1
	}
	return 0
}

func validPhotosAccessStatus(status string) bool {
	switch status {
	case "not_determined", "restricted", "denied", "authorized", "limited":
		return true
	default:
		return false
	}
}

func runWireRequest(ctx context.Context, args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requestPath := flags.String("request", "", "protobuf request path")
	responsePath := flags.String("response", "", "protobuf response path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *requestPath == "" || *responsePath == "" {
		writeln(stderr, "photoscrawl-fetch run: --request and --response are required; positional arguments are not accepted")
		return 2
	}
	data, err := readWireRequest(*requestPath)
	if err != nil {
		_ = writeWireResponse(*responsePath, failedWireResponse("invalid_request", "PhotoKit request could not be read", nil))
		return 1
	}
	var request fetchwire.OriginalFetchRequest
	if err := proto.Unmarshal(data, &request); err != nil || request.LocalIdentifier == "" || request.DestinationPath == "" {
		_ = writeWireResponse(*responsePath, failedWireResponse("invalid_request", "PhotoKit request is invalid", nil))
		return 1
	}
	timeout := time.Duration(request.TimeoutMilliseconds) * time.Millisecond
	if timeout <= 0 || timeout > 10*time.Minute {
		_ = writeWireResponse(*responsePath, failedWireResponse("invalid_request", "PhotoKit request timeout is invalid", nil))
		return 1
	}
	exportCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = exportOriginalMatching(exportCtx, photos.OriginalExportQuery{
		LocalIdentifier:  request.LocalIdentifier,
		CreationDate:     request.CreationDate,
		Width:            request.Width,
		Height:           request.Height,
		OriginalFilename: request.OriginalFilename,
	}, request.DestinationPath, request.AllowNetwork)
	if err != nil {
		_ = os.Remove(request.DestinationPath)
		_ = os.Remove(request.DestinationPath + ".exporting")
		_ = writeWireResponse(*responsePath, wireErrorResponse(err))
		return 1
	}
	info, digest, err := photos.InspectOriginalFile(request.DestinationPath)
	if err != nil {
		_ = os.Remove(request.DestinationPath)
		_ = writeWireResponse(*responsePath, failedWireResponse("invalid_output", "PhotoKit returned an invalid original", nil))
		return 1
	}
	if err := writeWireResponse(*responsePath, &fetchwire.OriginalFetchResponse{
		Success:   true,
		SizeBytes: info.Size(),
		Sha256:    digest[:],
	}); err != nil {
		_ = os.Remove(request.DestinationPath)
		return 1
	}
	return 0
}

func runCurrentStillWireRequest(ctx context.Context, args []string, stderr io.Writer) int {
	helperStartedAt := time.Now()
	requestPath, responsePath, ok := wirePaths(args, stderr)
	if !ok {
		return 2
	}
	data, err := readWireRequest(requestPath)
	if err != nil {
		_ = writeCurrentStillWireResponse(responsePath, startedCurrentStillFailure(helperStartedAt, "invalid_request", "PhotoKit current-still request could not be read"))
		return 1
	}
	var request fetchwire.CurrentStillFetchRequest
	if err := proto.Unmarshal(data, &request); err != nil || request.SourceLibraryId == "" || request.AssetUuid == "" || request.DestinationPath == "" || (request.HasExpectedModification && (request.ModificationUnixSeconds <= 0 || request.ModificationMicroseconds < 0 || request.ModificationMicroseconds >= 1_000_000)) || (!request.HasExpectedModification && (request.ModificationUnixSeconds != 0 || request.ModificationMicroseconds != 0)) {
		_ = writeCurrentStillWireResponse(responsePath, startedCurrentStillFailure(helperStartedAt, "invalid_request", "PhotoKit current-still request is invalid"))
		return 1
	}
	timeout := time.Duration(request.TimeoutMilliseconds) * time.Millisecond
	if timeout <= 0 || timeout > 10*time.Minute {
		_ = writeCurrentStillWireResponse(responsePath, startedCurrentStillFailure(helperStartedAt, "invalid_request", "PhotoKit current-still request timeout is invalid"))
		return 1
	}
	exportCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fact, err := exportCurrentStill(exportCtx, photos.CurrentStillNativeRequest{AssetUUID: request.AssetUuid, HasExpectedModification: request.HasExpectedModification, Modification: photos.CurrentStillModification{UnixSeconds: request.ModificationUnixSeconds, Microseconds: request.ModificationMicroseconds}, AllowNetwork: request.AllowNetwork}, request.DestinationPath)
	if err != nil {
		_ = os.Remove(request.DestinationPath)
		_ = os.Remove(request.DestinationPath + ".exporting")
		response := currentStillWireErrorResponse(err)
		response.HelperStartedUnixNanos = helperStartedAt.UnixNano()
		applyCurrentStillErrorTimings(response, err)
		_ = writeCurrentStillWireResponse(responsePath, response)
		return 1
	}
	if err := writeCurrentStillWireResponse(responsePath, &fetchwire.CurrentStillFetchResponse{Success: true, SizeBytes: fact.Size, Sha256: mustDecodeDigest(fact.SHA256), MediaType: fact.MediaType, Orientation: fact.Orientation, PixelWidth: fact.PixelWidth, PixelHeight: fact.PixelHeight, HelperStartedUnixNanos: helperStartedAt.UnixNano(), PhotokitCallbackMicros: fact.Timings.PhotoKitCallbackMicros, ValidationHashMicros: fact.Timings.ValidationHashMicros, PhotokitCalls: int32(fact.PhotoKitCalls)}); err != nil {
		_ = os.Remove(request.DestinationPath)
		return 1
	}
	return 0
}

func startedCurrentStillFailure(startedAt time.Time, kind, message string) *fetchwire.CurrentStillFetchResponse {
	response := failedCurrentStillResponse(kind, message, nil)
	response.HelperStartedUnixNanos = startedAt.UnixNano()
	return response
}

func applyCurrentStillErrorTimings(response *fetchwire.CurrentStillFetchResponse, err error) {
	var measured *photos.CurrentStillMeasuredError
	if !errors.As(err, &measured) {
		return
	}
	response.PhotokitCallbackMicros = measured.Timings.PhotoKitCallbackMicros
	response.ValidationHashMicros = measured.Timings.ValidationHashMicros
	response.PhotokitCalls = int32(measured.PhotoKitCalls)
}

func wirePaths(args []string, stderr io.Writer) (string, string, bool) {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requestPath := flags.String("request", "", "protobuf request path")
	responsePath := flags.String("response", "", "protobuf response path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *requestPath == "" || *responsePath == "" {
		writeln(stderr, "photoscrawl-fetch run: --request and --response are required; positional arguments are not accepted")
		return "", "", false
	}
	return *requestPath, *responsePath, true
}

func mustDecodeDigest(hexDigest string) []byte {
	data, _ := hex.DecodeString(hexDigest)
	return data
}

func currentStillWireErrorResponse(err error) *fetchwire.CurrentStillFetchResponse {
	var accessErr *photos.PhotoLibraryAccessError
	var exportErr *photos.PhotoKitExportError
	var stageErr *photos.CurrentStillStageError
	switch {
	case errors.As(err, &accessErr):
		return failedCurrentStillResponse("photos_access", accessErr.Error(), accessErr)
	case errors.Is(err, photos.ErrPhotoKitAssetNotFound):
		return failedCurrentStillResponse("asset_not_found", "PhotoKit could not find the selected asset", nil)
	case errors.As(err, &exportErr):
		safe := photos.NewPhotoKitCallbackError(exportErr.Domain, exportErr.Code, exportErr.Reason, exportErr.CallbackCancelled, exportErr.CallbackDegraded, exportErr.CallbackInCloud, exportErr.CallbackReturned)
		safe.CallbackTimedOut = exportErr.CallbackTimedOut
		kind := "photokit_export"
		if safe.CallbackTimedOut {
			kind = "timeout"
		}
		response := failedCurrentStillResponse(kind, safe.Reason+" ("+safe.CallbackFacts()+")", nil)
		response.ErrorDomain = safe.Domain
		response.ErrorCode = safe.Code
		return response
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, photos.ErrPhotoKitExportTimedOut):
		return failedCurrentStillResponse("timeout", "PhotoKit current-still request timed out", nil)
	case errors.As(err, &stageErr):
		return failedCurrentStillResponse("export_failed", fmt.Sprintf("PhotoKit current-still request failed (stage=%s)", stageErr.Stage()), nil)
	default:
		return failedCurrentStillResponse("export_failed", "PhotoKit current-still request failed", nil)
	}
}

func failedCurrentStillResponse(kind, message string, accessErr *photos.PhotoLibraryAccessError) *fetchwire.CurrentStillFetchResponse {
	response := &fetchwire.CurrentStillFetchResponse{FailureKind: kind, ErrorMessage: message}
	if accessErr != nil {
		response.PhotosAccessStatus = accessErr.Status
	}
	return response
}

func writeCurrentStillWireResponse(path string, response *fetchwire.CurrentStillFetchResponse) error {
	data, err := proto.Marshal(response)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".writing"
	_ = os.Remove(temporary)
	defer func() { _ = os.Remove(temporary) }()
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func readWireRequest(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 64*1024 {
		return nil, errors.New("PhotoKit request exceeds 64KB")
	}
	return data, nil
}

func wireErrorResponse(err error) *fetchwire.OriginalFetchResponse {
	var accessErr *photos.PhotoLibraryAccessError
	var exportErr *photos.PhotoKitExportError
	switch {
	case errors.As(err, &accessErr):
		return failedWireResponse("photos_access", accessErr.Error(), accessErr)
	case errors.Is(err, photos.ErrPhotoKitAssetNotFound):
		return failedWireResponse("asset_not_found", "PhotoKit could not find the selected asset", nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, photos.ErrPhotoKitExportTimedOut):
		return failedWireResponse("timeout", "PhotoKit original export timed out", nil)
	case errors.As(err, &exportErr):
		safeError := photos.NewPhotoKitExportError(exportErr.Domain, exportErr.Code, exportErr.Reason)
		response := failedWireResponse("photokit_export", safeError.Reason, nil)
		response.ErrorDomain = safeError.Domain
		response.ErrorCode = safeError.Code
		return response
	default:
		return failedWireResponse("export_failed", "PhotoKit original export failed", nil)
	}
}

func failedWireResponse(kind, message string, accessErr *photos.PhotoLibraryAccessError) *fetchwire.OriginalFetchResponse {
	response := &fetchwire.OriginalFetchResponse{FailureKind: kind, ErrorMessage: message}
	if accessErr != nil {
		response.PhotosAccessStatus = accessErr.Status
	}
	return response
}

func writeWireResponse(path string, response *fetchwire.OriginalFetchResponse) error {
	data, err := proto.Marshal(response)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tempPath := path + ".writing"
	_ = os.Remove(tempPath)
	defer func() { _ = os.Remove(tempPath) }()
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}
