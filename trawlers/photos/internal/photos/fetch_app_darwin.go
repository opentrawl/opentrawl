//go:build darwin

package photos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos/fetchwire"
	"google.golang.org/protobuf/proto"
)

const (
	photoKitFetchBundleID       = "org.opentrawl.photoscrawl.fetch"
	photoKitFetchExecutable     = "photoscrawl-fetch"
	photoKitPhotosEntitlement   = "com.apple.security.personal-information.photos-library"
	defaultPhotoKitFetchTimeout = 2 * time.Minute
	photoKitAccessTimeout       = 5 * time.Minute
	maxPhotoKitWireBytes        = 64 * 1024
)

var resolvePhotoKitFetchApp = verifiedPhotoKitFetchAppPath

var runPhotoKitFetchOpen = func(ctx context.Context, appPath, requestPath, responsePath string) error {
	command := exec.CommandContext(ctx, "/usr/bin/open", photoKitFetchOpenArgs(appPath, requestPath, responsePath)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := bytes.TrimSpace(stderr.Bytes())
		if len(message) > 0 {
			return fmt.Errorf("launch signed Photos original fetch app: %w: %s", err, message)
		}
		return fmt.Errorf("launch signed Photos original fetch app: %w", err)
	}
	return nil
}

var launchPhotoKitFetchApp = func(ctx context.Context, requestPath, responsePath string) error {
	appPath, err := resolvePhotoKitFetchApp(ctx)
	if err != nil {
		return err
	}
	return runPhotoKitFetchOpen(ctx, appPath, requestPath, responsePath)
}

var launchPhotoKitPermissionApp = func(ctx context.Context, appPath, operation, responsePath string) error {
	command := exec.CommandContext(ctx, "/usr/bin/open", photoKitFetchPermissionOpenArgs(appPath, operation, responsePath)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := bytes.TrimSpace(stderr.Bytes())
		if len(message) > 0 {
			return fmt.Errorf("launch signed Photos permission app: %w: %s", err, message)
		}
		return fmt.Errorf("launch signed Photos permission app: %w", err)
	}
	return nil
}

var launchPhotoKitCurrentStillApp = func(ctx context.Context, appPath, requestPath, responsePath string) error {
	command := exec.CommandContext(ctx, "/usr/bin/open", photoKitFetchCurrentStillOpenArgs(appPath, requestPath, responsePath)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := bytes.TrimSpace(stderr.Bytes())
		if len(message) > 0 {
			return fmt.Errorf("launch signed Photos current-still fetch app: %w: %s", err, message)
		}
		return fmt.Errorf("launch signed Photos current-still fetch app: %w", err)
	}
	return nil
}

var launchPhotoKitReadinessApp = func(ctx context.Context, appPath, requestPath, responsePath string) error {
	command := exec.CommandContext(ctx, "/usr/bin/open", photoKitFetchReadinessOpenArgs(appPath, requestPath, responsePath)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := bytes.TrimSpace(stderr.Bytes())
		if len(message) > 0 {
			return fmt.Errorf("launch signed Photos readiness app: %w: %s", err, message)
		}
		return fmt.Errorf("launch signed Photos readiness app: %w", err)
	}
	return nil
}

func verifiedPhotoKitFetchAppPath(ctx context.Context) (string, error) {
	callerPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve Photos helper caller: %w", err)
	}
	callerPath, err = filepath.EvalSymlinks(callerPath)
	if err != nil {
		return "", fmt.Errorf("resolve Photos helper caller path: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for Photos original fetch app: %w", err)
	}
	return verifiedPhotoKitFetchAppPathForCaller(ctx, callerPath, home)
}

func verifiedPhotoKitFetchAppPathForCaller(ctx context.Context, callerPath, home string) (string, error) {
	appPath, err := photoKitFetchAppPath(callerPath, home)
	if err != nil {
		return "", err
	}
	if err := verifyPhotoKitFetchApp(ctx, callerPath, appPath); err != nil {
		return "", err
	}
	return appPath, nil
}

func photoKitFetchAppPath(callerPath, home string) (string, error) {
	callerPath = filepath.Clean(callerPath)
	callerName := filepath.Base(callerPath)
	callerDir := filepath.Dir(callerPath)
	contentsDir := filepath.Dir(callerDir)
	appDir := filepath.Dir(contentsDir)
	isOpenTrawlBundle := filepath.Base(contentsDir) == "Contents" && filepath.Base(appDir) == "OpenTrawl.app"

	switch {
	case callerName == "Trawl" && filepath.Base(callerDir) == "MacOS" && isOpenTrawlBundle:
		return filepath.Join(contentsDir, "Helpers", "Photoscrawl Fetch.app"), nil
	case callerName == "trawl" && filepath.Base(callerDir) == "Helpers" && isOpenTrawlBundle:
		return filepath.Join(contentsDir, "Helpers", "Photoscrawl Fetch.app"), nil
	case callerName == "Trawl":
		return "", errors.New("Photos helper caller is not the OpenTrawl Mac app executable")
	case callerName == "trawl" && filepath.Base(callerDir) == "Helpers" && filepath.Base(contentsDir) == "Contents":
		return "", errors.New("Photos helper caller is not bundled in OpenTrawl.app")
	case callerName == "trawl":
		return filepath.Join(home, "Applications", "Photoscrawl Fetch.app"), nil
	default:
		return "", errors.New("Photos helper caller is not a supported OpenTrawl executable")
	}
}

func verifyPhotoKitFetchApp(ctx context.Context, callerPath, appPath string) error {
	info, err := os.Stat(appPath)
	if err != nil {
		return fmt.Errorf("find signed Photos original fetch app: %w", err)
	}
	if !info.IsDir() {
		return errors.New("signed Photos original fetch app is not an app bundle")
	}
	helperExecutable := filepath.Join(appPath, "Contents", "MacOS", photoKitFetchExecutable)
	executableInfo, err := os.Stat(helperExecutable)
	if err != nil {
		return fmt.Errorf("find signed Photos helper executable: %w", err)
	}
	if !executableInfo.Mode().IsRegular() || executableInfo.Mode().Perm()&0o111 == 0 {
		return errors.New("signed Photos helper executable is not executable")
	}

	identifier, err := photoKitFetchPlistValue(ctx, appPath, "CFBundleIdentifier")
	if err != nil {
		return err
	}
	if identifier != photoKitFetchBundleID {
		return fmt.Errorf("signed Photos helper bundle identifier is %q, want %q", identifier, photoKitFetchBundleID)
	}
	executable, err := photoKitFetchPlistValue(ctx, appPath, "CFBundleExecutable")
	if err != nil {
		return err
	}
	if executable != photoKitFetchExecutable {
		return fmt.Errorf("signed Photos helper executable is %q, want %q", executable, photoKitFetchExecutable)
	}

	matches, err := codeSigningLeafIdentityMatches(ctx, callerPath, appPath)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("signed Photos helper leaf certificate does not match its caller")
	}
	if err := verifyPhotoKitCodeSignature(ctx, callerPath, false); err != nil {
		return fmt.Errorf("verify Photos helper caller signature: %w", err)
	}
	if err := verifyPhotoKitCodeSignature(ctx, appPath, true); err != nil {
		return fmt.Errorf("verify signed Photos helper signature and identity: %w", err)
	}
	if err := verifyPhotoKitFetchEntitlement(ctx, appPath); err != nil {
		return err
	}
	return nil
}

func verifyPhotoKitFetchEntitlement(ctx context.Context, appPath string) error {
	output, err := runPhotoKitFetchCombinedCommand(ctx, "/usr/bin/codesign", "--display", "--entitlements", "-", appPath)
	if err != nil {
		return fmt.Errorf("read signed Photos helper entitlements: %w", err)
	}
	lines := strings.Split(string(output), "\n")
	keyLine := "[Key] " + photoKitPhotosEntitlement
	found := 0
	for index, line := range lines {
		if strings.TrimSpace(line) != keyLine {
			continue
		}
		found++
		if index+2 >= len(lines) || strings.TrimSpace(lines[index+1]) != "[Value]" || strings.TrimSpace(lines[index+2]) != "[Bool] true" {
			return errors.New("signed Photos helper Photos entitlement is not true")
		}
	}
	if found != 1 {
		return errors.New("signed Photos helper must contain exactly one true Photos entitlement")
	}
	return nil
}

func photoKitFetchPlistValue(ctx context.Context, appPath, key string) (string, error) {
	infoPath := filepath.Join(appPath, "Contents", "Info.plist")
	output, err := runPhotoKitFetchCommand(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", infoPath)
	if err != nil {
		return "", fmt.Errorf("read signed Photos helper %s: %w", key, err)
	}
	return string(bytes.TrimSpace(output)), nil
}

func verifyPhotoKitCodeSignature(ctx context.Context, path string, deep bool) error {
	args := []string{"--verify", "--strict"}
	if deep {
		args = append(args, "--deep")
	}
	args = append(args, path)
	output, err := runPhotoKitFetchCombinedCommand(ctx, "/usr/bin/codesign", args...)
	if err == nil {
		return nil
	}
	if bytes.Contains(output, []byte("CSSMERR_TP_NOT_TRUSTED")) {
		return nil
	}
	return err
}

func runPhotoKitFetchCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := bytes.TrimSpace(stderr.Bytes())
		if len(message) > 0 {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

func runPhotoKitFetchCombinedCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := bytes.TrimSpace(output)
		if len(message) > 0 {
			return output, fmt.Errorf("%w: %s", err, message)
		}
		return output, err
	}
	return output, nil
}

func photoKitFetchOpenArgs(appPath, requestPath, responsePath string) []string {
	return []string{
		"-n", "-g", appPath,
		"--args", "run", "--request", requestPath, "--response", responsePath,
	}
}

func photoKitFetchCurrentStillOpenArgs(appPath, requestPath, responsePath string) []string {
	return []string{
		"-n", "-g", appPath,
		"--args", "run-current-still", "--request", requestPath, "--response", responsePath,
	}
}

func photoKitFetchReadinessOpenArgs(appPath, requestPath, responsePath string) []string {
	return []string{
		"-n", "-g", appPath,
		"--args", "run-readiness", "--request", requestPath, "--response", responsePath,
	}
}

func photoKitFetchPermissionOpenArgs(appPath, operation, responsePath string) []string {
	args := []string{
		"-n", appPath,
		"--args", "permission", operation, "--response", responsePath,
	}
	if operation != "request" {
		args = append([]string{"-g"}, args...)
	}
	return args
}

// PhotoLibraryAccessStatusThroughApp reads, and only when requested asks for,
// Photos access through the verified helper identity.
func PhotoLibraryAccessStatusThroughApp(ctx context.Context, request bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	appPath, err := resolvePhotoKitFetchApp(ctx)
	if err != nil {
		return "", err
	}
	wireDir, err := os.MkdirTemp("", ".photokit-access-*")
	if err != nil {
		return "", fmt.Errorf("create Photos access response directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(wireDir) }()
	responsePath := filepath.Join(wireDir, "response.pb")
	operation := "status"
	if request {
		operation = "request"
	}
	launchCtx, cancel := context.WithTimeout(context.Background(), photoKitAccessTimeout+10*time.Second)
	defer cancel()
	if err := launchPhotoKitPermissionApp(launchCtx, appPath, operation, responsePath); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	responseData, err := waitForBoundedFile(launchCtx, responsePath, maxPhotoKitWireBytes)
	if err != nil {
		return "", fmt.Errorf("read Photos access response: %w", err)
	}
	var response fetchwire.OriginalFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		return "", fmt.Errorf("decode Photos access response: %w", err)
	}
	if !response.Success {
		return "", &PhotoLibraryAccessFailure{Kind: response.GetFailureKind(), Message: response.GetErrorMessage()}
	}
	if !validPhotoLibraryAccessStatus(response.GetPhotosAccessStatus()) {
		return "", &PhotoLibraryAccessFailure{Kind: "native_status", Message: "PhotoKit returned an unrecognised Photos access state"}
	}
	return response.GetPhotosAccessStatus(), nil
}

func validPhotoLibraryAccessStatus(status string) bool {
	switch status {
	case "not_determined", "restricted", "denied", "authorized", "limited":
		return true
	default:
		return false
	}
}

// AssetReadinessThroughApp asks the verified helper to resolve one archive
// UUID and return its identity and resource facts without reading media bytes.
func AssetReadinessThroughApp(ctx context.Context, assetUUID string) (AssetReadiness, error) {
	if err := ctx.Err(); err != nil {
		return AssetReadiness{}, err
	}
	appPath, err := resolvePhotoKitFetchApp(ctx)
	if err != nil {
		return AssetReadiness{}, err
	}
	wireDir, err := os.MkdirTemp("", ".photokit-readiness-*")
	if err != nil {
		return AssetReadiness{}, fmt.Errorf("create Photos readiness directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(wireDir) }()
	requestPath := filepath.Join(wireDir, "request.pb")
	responsePath := filepath.Join(wireDir, "response.pb")
	data, err := proto.Marshal(&fetchwire.AssetReadinessRequest{AssetUuid: assetUUID})
	if err != nil {
		return AssetReadiness{}, fmt.Errorf("encode Photos readiness request: %w", err)
	}
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		return AssetReadiness{}, fmt.Errorf("write Photos readiness request: %w", err)
	}
	launchCtx, cancel := context.WithTimeout(context.Background(), defaultPhotoKitFetchTimeout+10*time.Second)
	defer cancel()
	if err := launchPhotoKitReadinessApp(launchCtx, appPath, requestPath, responsePath); err != nil {
		if ctx.Err() != nil {
			return AssetReadiness{}, ctx.Err()
		}
		return AssetReadiness{}, err
	}
	if ctx.Err() != nil {
		return AssetReadiness{}, ctx.Err()
	}
	responseData, err := waitForBoundedFile(launchCtx, responsePath, maxPhotoKitWireBytes)
	if err != nil {
		return AssetReadiness{}, fmt.Errorf("read Photos readiness response: %w", err)
	}
	var response fetchwire.AssetReadinessResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		return AssetReadiness{}, fmt.Errorf("decode Photos readiness response: %w", err)
	}
	if !response.Success {
		return AssetReadiness{}, photoKitReadinessAppFailure(&response)
	}
	readiness := AssetReadiness{
		LocalIdentifier: response.LocalIdentifier, AssetUUID: response.AssetUuid,
		MediaType: response.MediaType, HasLocation: response.HasLocation,
		CreationDate: response.CreationDate, ModificationDate: response.ModificationDate,
		PixelWidth: response.PixelWidth, PixelHeight: response.PixelHeight,
		OriginalFilename: response.OriginalFilename, OriginalUTI: response.OriginalUti,
	}
	if readiness.AssetUUID == "" || readiness.LocalIdentifier == "" || readiness.MediaType != "image" || readiness.HasLocation || readiness.OriginalFilename == "" {
		return AssetReadiness{}, errors.New("signed Photos readiness app returned incomplete asset facts")
	}
	return readiness, nil
}

func photoKitReadinessAppFailure(response *fetchwire.AssetReadinessResponse) error {
	if response.GetFailureKind() == "photos_access" {
		return &PhotoLibraryAccessFailure{Kind: "photos_access", Message: response.GetPhotosAccessStatus()}
	}
	if response.GetFailureKind() == "no_compatible_asset" {
		return errors.New("signed Photos readiness app found no unlocated image with an immutable-original resource")
	}
	return errors.New("signed Photos readiness app could not select a compatible image")
}

// ExportOriginalResourceThroughApp exports one preferred original through the
// signed LaunchServices app that owns the Photos permission grant.
func ExportOriginalResourceThroughApp(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	timeout := defaultPhotoKitFetchTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
	}
	wireDir, err := os.MkdirTemp(filepath.Dir(destinationPath), ".photokit-request-*")
	if err != nil {
		return fmt.Errorf("create PhotoKit request directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(wireDir) }()
	requestPath := filepath.Join(wireDir, "request.pb")
	responsePath := filepath.Join(wireDir, "response.pb")
	request := &fetchwire.OriginalFetchRequest{
		LocalIdentifier:     query.LocalIdentifier,
		CreationDate:        query.CreationDate,
		Width:               query.Width,
		Height:              query.Height,
		OriginalFilename:    query.OriginalFilename,
		DestinationPath:     destinationPath,
		AllowNetwork:        allowNetwork,
		TimeoutMilliseconds: timeout.Milliseconds(),
	}
	data, err := proto.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode PhotoKit request: %w", err)
	}
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		return fmt.Errorf("write PhotoKit request: %w", err)
	}

	// LaunchServices owns the signed app process. Cancelling /usr/bin/open would
	// not cancel that app, so keep waiting for its bounded request to finish
	// before removing the wire directory or media output.
	launchCtx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
	defer cancel()
	if err := launchPhotoKitFetchApp(launchCtx, requestPath, responsePath); err != nil {
		removeOriginalOutput(destinationPath)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if launchCtx.Err() != nil {
			return launchCtx.Err()
		}
		return err
	}
	if ctx.Err() != nil {
		removeOriginalOutput(destinationPath)
		return ctx.Err()
	}
	responseData, err := waitForBoundedFile(launchCtx, responsePath, maxPhotoKitWireBytes)
	if err != nil {
		removeOriginalOutput(destinationPath)
		return fmt.Errorf("read PhotoKit response: %w", err)
	}
	var response fetchwire.OriginalFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		removeOriginalOutput(destinationPath)
		return fmt.Errorf("decode PhotoKit response: %w", err)
	}
	if !response.Success {
		removeOriginalOutput(destinationPath)
		return photoKitAppFailure(&response)
	}
	info, digest, err := InspectOriginalFile(destinationPath)
	if err != nil {
		removeOriginalOutput(destinationPath)
		return fmt.Errorf("inspect PhotoKit output: %w", err)
	}
	if response.SizeBytes != info.Size() || !bytes.Equal(response.Sha256, digest[:]) {
		removeOriginalOutput(destinationPath)
		return errors.New("signed Photos original fetch app returned mismatched output proof")
	}
	return nil
}

// ExportCurrentStillThroughApp uses the same verified helper identity as the
// original path, but sends a distinct protobuf and helper mode. Network use is
// represented only by request.AllowNetwork.
func ExportCurrentStillThroughApp(ctx context.Context, request CurrentStillRequest, destinationPath string) (CurrentStillFact, error) {
	timings := CurrentStillPhaseTimings{}
	if err := ctx.Err(); err != nil {
		return CurrentStillFact{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return failedCurrentStillFact(err, timings)
	}
	timeout := defaultPhotoKitFetchTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return failedCurrentStillFact(context.DeadlineExceeded, timings)
		}
	}
	verificationStartedAt := time.Now()
	appPath, err := resolvePhotoKitFetchApp(ctx)
	timings.HelperVerificationMicros = elapsedMicros(verificationStartedAt)
	if err != nil {
		return failedCurrentStillFact(err, timings)
	}
	wireDir, err := os.MkdirTemp(filepath.Dir(destinationPath), ".photokit-current-still-request-*")
	if err != nil {
		return failedCurrentStillFact(fmt.Errorf("create current-still request directory: %w", err), timings)
	}
	defer func() { _ = os.RemoveAll(wireDir) }()
	requestPath := filepath.Join(wireDir, "request.pb")
	responsePath := filepath.Join(wireDir, "response.pb")
	modification, hasExpectedModification := request.Freshness.ExpectedModification()
	photoKitIdentifier := request.PhotoKitLocalIdentifier
	if photoKitIdentifier == "" {
		photoKitIdentifier = request.AssetUUID
	}
	data, err := proto.Marshal(&fetchwire.CurrentStillFetchRequest{
		SourceLibraryId: request.SourceLibraryID, AssetUuid: photoKitIdentifier,
		ModificationUnixSeconds: modification.UnixSeconds, DestinationPath: destinationPath,
		AllowNetwork: request.AllowNetwork, TimeoutMilliseconds: timeout.Milliseconds(), ModificationMicroseconds: modification.Microseconds,
		HasExpectedModification: hasExpectedModification,
	})
	if err != nil {
		return failedCurrentStillFact(fmt.Errorf("encode current-still request: %w", err), timings)
	}
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		return failedCurrentStillFact(fmt.Errorf("write current-still request: %w", err), timings)
	}
	if err := ctx.Err(); err != nil {
		return failedCurrentStillFact(err, timings)
	}
	launchCtx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
	defer cancel()
	launchStartedAt := time.Now()
	if err := launchPhotoKitCurrentStillApp(launchCtx, appPath, requestPath, responsePath); err != nil {
		timings.LaunchServicesStartMicros = elapsedMicros(launchStartedAt)
		removeOriginalOutput(destinationPath)
		if ctx.Err() != nil {
			return failedCurrentStillFact(ctx.Err(), timings)
		}
		return failedCurrentStillFact(err, timings)
	}
	timings.LaunchServicesStartMicros = elapsedMicros(launchStartedAt)
	responseData, err := waitForBoundedFile(launchCtx, responsePath, maxPhotoKitWireBytes)
	if err != nil {
		removeOriginalOutput(destinationPath)
		return failedCurrentStillFact(fmt.Errorf("read current-still response: %w", err), timings)
	}
	var response fetchwire.CurrentStillFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		removeOriginalOutput(destinationPath)
		return failedCurrentStillFact(fmt.Errorf("decode current-still response: %w", err), timings)
	}
	if response.HelperStartedUnixNanos >= launchStartedAt.UnixNano() {
		timings.LaunchServicesStartMicros = max((response.HelperStartedUnixNanos-launchStartedAt.UnixNano()+int64(time.Microsecond)-1)/int64(time.Microsecond), 1)
	}
	timings.PhotoKitCallbackMicros = response.PhotokitCallbackMicros
	timings.ValidationHashMicros = response.ValidationHashMicros
	photoKitCalls := int(response.PhotokitCalls)
	if ctx.Err() != nil {
		removeOriginalOutput(destinationPath)
		return failedCurrentStillFactWithCalls(ctx.Err(), timings, photoKitCalls)
	}
	if !response.Success {
		removeOriginalOutput(destinationPath)
		return failedCurrentStillFactWithCalls(currentStillAppFailure(&response), timings, photoKitCalls)
	}
	validationStartedAt := time.Now()
	info, digest, err := InspectOriginalFile(destinationPath)
	if err != nil {
		timings.ValidationHashMicros += elapsedMicros(validationStartedAt)
		removeOriginalOutput(destinationPath)
		return failedCurrentStillFactWithCalls(fmt.Errorf("inspect current-still output: %w", err), timings, photoKitCalls)
	}
	fact := CurrentStillFact{MediaType: response.MediaType, Orientation: response.Orientation, PixelWidth: response.PixelWidth, PixelHeight: response.PixelHeight, Size: response.SizeBytes, SHA256: fmt.Sprintf("%x", response.Sha256), PhotoKitCalls: photoKitCalls}
	if fact.Size != info.Size() || !bytes.Equal(response.Sha256, digest[:]) || fact.MediaType == "" || fact.PixelWidth <= 0 || fact.PixelHeight <= 0 {
		timings.ValidationHashMicros += elapsedMicros(validationStartedAt)
		removeOriginalOutput(destinationPath)
		return failedCurrentStillFactWithCalls(errors.New("signed Photos current-still fetch app returned mismatched output proof"), timings, photoKitCalls)
	}
	timings.ValidationHashMicros += elapsedMicros(validationStartedAt)
	fact.Timings = timings
	return fact, nil
}

func failedCurrentStillFact(err error, timings CurrentStillPhaseTimings) (CurrentStillFact, error) {
	return failedCurrentStillFactWithCalls(err, timings, 0)
}

func failedCurrentStillFactWithCalls(err error, timings CurrentStillPhaseTimings, photoKitCalls int) (CurrentStillFact, error) {
	failure := measuredCurrentStillError(err, timings)
	failure.PhotoKitCalls = photoKitCalls
	return CurrentStillFact{}, failure
}

func currentStillAppFailure(response *fetchwire.CurrentStillFetchResponse) error {
	switch response.GetFailureKind() {
	case "photos_access":
		return &PhotoLibraryAccessError{Status: response.GetPhotosAccessStatus()}
	case "asset_not_found":
		return ErrPhotoKitAssetNotFound
	case "timeout":
		return ErrPhotoKitExportTimedOut
	case "photokit_export":
		return NewPhotoKitExportError(response.GetErrorDomain(), response.GetErrorCode(), response.GetErrorMessage())
	default:
		if response.GetErrorMessage() == "" {
			return errors.New("signed Photos current-still fetch app failed")
		}
		return errors.New(response.GetErrorMessage())
	}
}

func photoKitAppFailure(response *fetchwire.OriginalFetchResponse) error {
	switch response.GetFailureKind() {
	case "photos_access":
		return &PhotoLibraryAccessError{Status: response.GetPhotosAccessStatus()}
	case "asset_not_found":
		return ErrPhotoKitAssetNotFound
	case "timeout":
		return ErrPhotoKitExportTimedOut
	case "photokit_export":
		return NewPhotoKitExportError(response.GetErrorDomain(), response.GetErrorCode(), response.GetErrorMessage())
	default:
		message := response.GetErrorMessage()
		if message == "" {
			message = "signed Photos original fetch app failed"
		}
		return errors.New(message)
	}
}

func removeOriginalOutput(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + ".exporting")
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func waitForBoundedFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	const retry = 25 * time.Millisecond
	for {
		data, err := readBoundedFile(path, limit)
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
