//go:build darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	oldExport := exportOriginalMatching
	exportOriginalMatching = func(_ context.Context, query photos.OriginalExportQuery, destination string, allowNetwork bool) error {
		if query.LocalIdentifier != "synthetic-asset" || query.OriginalFilename != "synthetic.heic" || !allowNetwork {
			t.Fatalf("query = %#v allow network = %t", query, allowNetwork)
		}
		return os.WriteFile(destination, []byte("exact synthetic original bytes"), 0o600)
	}
	defer func() { exportOriginalMatching = oldExport }()

	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.pb")
	responsePath := filepath.Join(dir, "response.pb")
	destination := filepath.Join(dir, "original.heic")
	requestData, err := proto.Marshal(&fetchwire.OriginalFetchRequest{
		LocalIdentifier:     "synthetic-asset",
		OriginalFilename:    "synthetic.heic",
		DestinationPath:     destination,
		AllowNetwork:        true,
		TimeoutMilliseconds: (2 * time.Minute).Milliseconds(),
	})
	if err != nil {
		t.Fatal(err)
	}
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
	var response fetchwire.OriginalFetchResponse
	if err := proto.Unmarshal(responseData, &response); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("exact synthetic original bytes"))
	if !response.Success || response.SizeBytes != 30 || !bytes.Equal(response.Sha256, digest[:]) {
		t.Fatalf("response = %#v", response)
	}
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
	if response.ErrorCode != "photos_access" || response.PhotosAccessStatus != "denied" {
		t.Fatalf("response = %#v", response)
	}
}
