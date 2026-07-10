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
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos/fetchwire"
	"google.golang.org/protobuf/proto"
)

const (
	photoKitFetchBundleID       = "org.opentrawl.photoscrawl.fetch"
	defaultPhotoKitFetchTimeout = 2 * time.Minute
	maxPhotoKitWireBytes        = 64 * 1024
)

var launchPhotoKitFetchApp = func(ctx context.Context, requestPath, responsePath string) error {
	command := exec.CommandContext(ctx, "/usr/bin/open", "-W", "-n", "-g", "-b", photoKitFetchBundleID,
		"--args", "run", "--request", requestPath, "--response", responsePath)
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

// ExportOriginalResourceThroughApp exports one preferred original through the
// signed LaunchServices app that owns the Photos permission grant.
func ExportOriginalResourceThroughApp(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
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

	launchCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		launchCtx, cancel = context.WithTimeout(ctx, timeout+10*time.Second)
	}
	defer cancel()
	if err := launchPhotoKitFetchApp(launchCtx, requestPath, responsePath); err != nil {
		removeOriginalOutput(destinationPath)
		if launchCtx.Err() != nil {
			return launchCtx.Err()
		}
		return err
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

func photoKitAppFailure(response *fetchwire.OriginalFetchResponse) error {
	switch response.GetErrorCode() {
	case "photos_access":
		return &PhotoLibraryAccessError{Status: response.GetPhotosAccessStatus()}
	case "asset_not_found":
		return ErrPhotoKitAssetNotFound
	case "timeout":
		return ErrPhotoKitExportTimedOut
	case "export_busy":
		return ErrExportAlreadyRunning
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
