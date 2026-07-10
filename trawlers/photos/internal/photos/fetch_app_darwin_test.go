//go:build darwin

package photos

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"

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

func TestExportOriginalResourceThroughAppReturnsTypedAccessFailure(t *testing.T) {
	oldLaunch := launchPhotoKitFetchApp
	launchPhotoKitFetchApp = func(_ context.Context, _, responsePath string) error {
		data, err := proto.Marshal(&fetchwire.OriginalFetchResponse{
			ErrorCode:          "photos_access",
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
