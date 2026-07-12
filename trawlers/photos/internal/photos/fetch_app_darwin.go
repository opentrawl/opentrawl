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

var launchPhotoKitCurrentStillApp = func(ctx context.Context, requestPath, responsePath string) error {
	appPath, err := resolvePhotoKitFetchApp(ctx)
	if err != nil {
		return err
	}
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

	callerLeafHash, err := codeSigningLeafDigest(callerPath)
	if err != nil {
		return fmt.Errorf("read Photos helper caller leaf certificate identity: %w", err)
	}
	helperLeafHash, err := codeSigningLeafDigest(appPath)
	if err != nil {
		return fmt.Errorf("read signed Photos helper leaf certificate identity: %w", err)
	}
	if callerLeafHash != helperLeafHash {
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
		"-W", "-n", "-g", appPath,
		"--args", "run", "--request", requestPath, "--response", responsePath,
	}
}

func photoKitFetchCurrentStillOpenArgs(appPath, requestPath, responsePath string) []string {
	return []string{
		"-W", "-n", "-g", appPath,
		"--args", "run-current-still", "--request", requestPath, "--response", responsePath,
	}
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
	responseData, err := readBoundedFile(responsePath, maxPhotoKitWireBytes)
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
	if err := ctx.Err(); err != nil {
		return CurrentStillFact{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return CurrentStillFact{}, err
	}
	timeout := defaultPhotoKitFetchTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return CurrentStillFact{}, context.DeadlineExceeded
		}
	}
	wireDir, err := os.MkdirTemp(filepath.Dir(destinationPath), ".photokit-current-still-request-*")
	if err != nil {
		return CurrentStillFact{}, fmt.Errorf("create current-still request directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(wireDir) }()
	requestPath := filepath.Join(wireDir, "request.pb")
	responsePath := filepath.Join(wireDir, "response.pb")
	data, err := proto.Marshal(&fetchwire.CurrentStillFetchRequest{
		SourceLibraryId: request.SourceLibraryID, AssetUuid: request.AssetUUID,
		ModificationUnixSeconds: request.Modification.UnixSeconds, DestinationPath: destinationPath,
		AllowNetwork: request.AllowNetwork, TimeoutMilliseconds: timeout.Milliseconds(), ModificationMicroseconds: request.Modification.Microseconds,
	})
	if err != nil {
		return CurrentStillFact{}, fmt.Errorf("encode current-still request: %w", err)
	}
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		return CurrentStillFact{}, fmt.Errorf("write current-still request: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return CurrentStillFact{}, err
	}
	launchCtx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
	defer cancel()
	if err := launchPhotoKitCurrentStillApp(launchCtx, requestPath, responsePath); err != nil {
		removeOriginalOutput(destinationPath)
		if ctx.Err() != nil {
			return CurrentStillFact{}, ctx.Err()
		}
		return CurrentStillFact{}, err
	}
	if ctx.Err() != nil {
		removeOriginalOutput(destinationPath)
		return CurrentStillFact{}, ctx.Err()
	}
	responseData, err := readBoundedFile(responsePath, maxPhotoKitWireBytes)
	if err != nil {
		removeOriginalOutput(destinationPath)
		return CurrentStillFact{}, fmt.Errorf("read current-still response: %w", err)
	}
	var response fetchwire.CurrentStillFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		removeOriginalOutput(destinationPath)
		return CurrentStillFact{}, fmt.Errorf("decode current-still response: %w", err)
	}
	if !response.Success {
		removeOriginalOutput(destinationPath)
		return CurrentStillFact{}, currentStillAppFailure(&response)
	}
	info, digest, err := InspectOriginalFile(destinationPath)
	if err != nil {
		removeOriginalOutput(destinationPath)
		return CurrentStillFact{}, fmt.Errorf("inspect current-still output: %w", err)
	}
	fact := CurrentStillFact{MediaType: response.MediaType, Orientation: response.Orientation, PixelWidth: response.PixelWidth, PixelHeight: response.PixelHeight, Size: response.SizeBytes, SHA256: fmt.Sprintf("%x", response.Sha256)}
	if fact.Size != info.Size() || !bytes.Equal(response.Sha256, digest[:]) || fact.MediaType == "" || fact.PixelWidth <= 0 || fact.PixelHeight <= 0 {
		removeOriginalOutput(destinationPath)
		return CurrentStillFact{}, errors.New("signed Photos current-still fetch app returned mismatched output proof")
	}
	return fact, nil
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
