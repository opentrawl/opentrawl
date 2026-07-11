//go:build darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos/fetchwire"
	"google.golang.org/protobuf/proto"
)

func TestNoArgumentLaunchOnlyRequestsAccess(t *testing.T) {
	oldRequest := requestAuthorization
	requestAuthorization = func(context.Context) (string, error) { return "authorized", nil }
	defer func() { requestAuthorization = oldRequest }()

	if exitCode := requestAccess(context.Background()); exitCode != 0 {
		t.Fatalf("exit code = %d", exitCode)
	}
}

func TestNoArgumentLaunchFailsWithoutReadAccess(t *testing.T) {
	oldRequest := requestAuthorization
	requestAuthorization = func(context.Context) (string, error) { return "denied", nil }
	defer func() { requestAuthorization = oldRequest }()

	if exitCode := requestAccess(context.Background()); exitCode != 1 {
		t.Fatalf("exit code = %d", exitCode)
	}
}

func TestDirectCommandsAreRejected(t *testing.T) {
	var stderr bytes.Buffer
	if exitCode := run(context.Background(), []string{"export"}, &stderr); exitCode != 2 {
		t.Fatalf("exit code = %d stderr = %q", exitCode, stderr.String())
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

func TestRunCurrentStillWireRequestPreservesExplicitNetworkAndFacts(t *testing.T) {
	oldExport := exportCurrentStill
	defer func() { exportCurrentStill = oldExport }()
	exportCurrentStill = func(_ context.Context, request photos.CurrentStillRequest, destination string) (photos.CurrentStillFact, error) {
		if request.SourceLibraryID != "synthetic-library" || request.AssetUUID != "synthetic-asset" || request.ModificationDate != "2026-07-11T12:00:00.125Z" || request.AllowNetwork {
			t.Fatalf("request = %#v", request)
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
	data, err := proto.Marshal(&fetchwire.CurrentStillFetchRequest{SourceLibraryId: "synthetic-library", AssetUuid: "synthetic-asset", ModificationDate: "2026-07-11T12:00:00.125Z", DestinationPath: destination, TimeoutMilliseconds: time.Minute.Milliseconds()})
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
	if !response.Success || response.MediaType != "public.heic" || response.PixelWidth != 4032 || response.PixelHeight != 3024 {
		t.Fatalf("response = %#v", response)
	}
}
