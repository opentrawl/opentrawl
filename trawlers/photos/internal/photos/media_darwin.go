//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework Foundation -framework AppKit -framework Photos -framework CoreLocation -framework CoreImage -framework CoreGraphics -framework ImageIO
#include <stdlib.h>

int photoscrawl_export_original_resource_matching(const char *localIdentifier, const char *creationDate, long long width, long long height, const char *originalFilename, const char *destinationPath, int allowNetwork, long long timeoutMilliseconds, char **errorOut, char **errorDomainOut, long long *errorCodeOut);
char *photoscrawl_photokit_authorization_status(char **errorOut);
char *photoscrawl_request_photokit_authorization(char **errorOut);
char *photoscrawl_asset_readiness(const char *assetUUID, char **errorOut);
int photoscrawl_location_is_valid(double latitude, double longitude);
int photoscrawl_render_canonical_jpeg(const char *sourcePath, const char *destinationPath, double quality, char **errorOut);
char *photoscrawl_image_metadata_record_json(const char *sourcePath, char **errorOut);
char *photoscrawl_image_metadata_typed_fixture_json(char **errorOut);
int photoscrawl_write_image_metadata_fixture(const char *destinationPath, char **errorOut);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// AssetReadiness is the signed-helper's small proof that one current,
// unlocated PhotoKit image has the identity and resource facts needed before
// any immutable-original or current-still export is attempted.
type AssetReadiness struct {
	LocalIdentifier  string `json:"local_identifier"`
	AssetUUID        string `json:"asset_uuid"`
	MediaType        string `json:"media_type"`
	HasLocation      bool   `json:"has_location"`
	CreationDate     string `json:"creation_date"`
	ModificationDate string `json:"modification_date"`
	PixelWidth       int64  `json:"pixel_width"`
	PixelHeight      int64  `json:"pixel_height"`
	OriginalFilename string `json:"original_filename"`
	OriginalUTI      string `json:"original_uti"`
}

func photoKitLocationIsValid(latitude, longitude float64) bool {
	return C.photoscrawl_location_is_valid(C.double(latitude), C.double(longitude)) != 0
}

// AssetReadinessForUUID resolves and reads identity and resource metadata for
// one live PhotoKit image. It does not request image bytes or network access.
func AssetReadinessForUUID(ctx context.Context, assetUUID string) (AssetReadiness, error) {
	if err := ctx.Err(); err != nil {
		return AssetReadiness{}, err
	}
	if CanonicalAssetUUID(assetUUID) == "" {
		return AssetReadiness{}, errors.New("PhotoKit asset readiness requires an asset UUID")
	}
	cUUID := C.CString(assetUUID)
	defer C.free(unsafe.Pointer(cUUID))
	var cErr *C.char
	cJSON := C.photoscrawl_asset_readiness(cUUID, &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return AssetReadiness{}, photoKitError(C.GoString(cErr))
	}
	if cJSON == nil {
		return AssetReadiness{}, errors.New("PhotoKit returned no asset readiness")
	}
	defer C.free(unsafe.Pointer(cJSON))
	var readiness AssetReadiness
	if err := json.Unmarshal([]byte(C.GoString(cJSON)), &readiness); err != nil {
		return AssetReadiness{}, fmt.Errorf("decode PhotoKit asset readiness: %w", err)
	}
	if readiness.AssetUUID == "" || readiness.LocalIdentifier == "" || readiness.MediaType != "image" || readiness.HasLocation || readiness.OriginalFilename == "" {
		return AssetReadiness{}, errors.New("PhotoKit returned incomplete asset readiness")
	}
	return readiness, nil
}

const photoLibraryAccessErrorPrefix = "photos_access:"

func ExportOriginalResourceMatching(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	lock, err := acquireExportLock(ctx, destinationPath)
	if err != nil {
		return err
	}
	defer lock.Close()

	tempDestination := destinationPath + ".exporting"
	_ = os.Remove(tempDestination)
	defer func() { _ = os.Remove(tempDestination) }()

	cIdentifier := C.CString(query.LocalIdentifier)
	defer C.free(unsafe.Pointer(cIdentifier))
	cCreationDate := C.CString(query.CreationDate)
	defer C.free(unsafe.Pointer(cCreationDate))
	cOriginalFilename := C.CString(query.OriginalFilename)
	defer C.free(unsafe.Pointer(cOriginalFilename))
	cDestination := C.CString(tempDestination)
	defer C.free(unsafe.Pointer(cDestination))

	var cErr *C.char
	var cErrorDomain *C.char
	var cErrorCode C.longlong
	timeout := defaultPhotoKitFetchTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
	}
	ok := C.photoscrawl_export_original_resource_matching(cIdentifier, cCreationDate, C.longlong(query.Width), C.longlong(query.Height), cOriginalFilename, cDestination, boolInt(allowNetwork), C.longlong(timeout.Milliseconds()), &cErr, &cErrorDomain, &cErrorCode)
	if cErrorDomain != nil {
		defer C.free(unsafe.Pointer(cErrorDomain))
		reason := ""
		if cErr != nil {
			reason = C.GoString(cErr)
		}
		if cErr != nil {
			defer C.free(unsafe.Pointer(cErr))
		}
		return NewPhotoKitExportError(C.GoString(cErrorDomain), int64(cErrorCode), reason)
	}
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return photoKitError(C.GoString(cErr))
	}
	if ok == 0 {
		return errors.New("export original resource failed")
	}
	if err := os.Rename(tempDestination, destinationPath); err != nil {
		return err
	}
	return nil
}

func photoKitError(message string) error {
	trimmed := strings.TrimSpace(message)
	if status, ok := strings.CutPrefix(trimmed, photoLibraryAccessErrorPrefix); ok {
		return &PhotoLibraryAccessError{Status: strings.TrimSpace(status)}
	}
	if strings.Contains(strings.ToLower(trimmed), "photokit asset not found") {
		if trimmed == "" || strings.EqualFold(trimmed, ErrPhotoKitAssetNotFound.Error()) {
			return ErrPhotoKitAssetNotFound
		}
		return fmt.Errorf("%w: %s", ErrPhotoKitAssetNotFound, trimmed)
	}
	if strings.EqualFold(trimmed, "PhotoKit asset is not an image") {
		return ErrPhotoKitAssetNotImage
	}
	if strings.EqualFold(trimmed, "PhotoKit asset has a valid location") {
		return ErrPhotoKitAssetHasLocation
	}
	if strings.EqualFold(trimmed, "PhotoKit asset has no image original resource") {
		return ErrPhotoKitOriginalMissing
	}
	if strings.Contains(strings.ToLower(trimmed), "photokit original export timed out") {
		return ErrPhotoKitExportTimedOut
	}
	return errors.New(trimmed)
}

// PhotoLibraryAuthorizationStatus reads the Photos access state without
// prompting or reading the library.
func PhotoLibraryAuthorizationStatus(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	var cErr *C.char
	cStatus := C.photoscrawl_photokit_authorization_status(&cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return "", photoKitError(C.GoString(cErr))
	}
	if cStatus == nil {
		return "", errors.New("PhotoKit returned no authorization status")
	}
	defer C.free(unsafe.Pointer(cStatus))
	return C.GoString(cStatus), nil
}

// RequestPhotoLibraryAuthorization may show the macOS Photos permission
// prompt. The signed helper calls it only after a status check finds that the
// decision is not determined.
func RequestPhotoLibraryAuthorization(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	var cErr *C.char
	cStatus := C.photoscrawl_request_photokit_authorization(&cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return "", photoKitError(C.GoString(cErr))
	}
	if cStatus == nil {
		return "", errors.New("PhotoKit returned no authorization status")
	}
	defer C.free(unsafe.Pointer(cStatus))
	return C.GoString(cStatus), nil
}

type exportLock struct {
	file *os.File
}

func acquireExportLock(ctx context.Context, destinationPath string) (*exportLock, error) {
	lockPath := filepath.Join(filepath.Dir(destinationPath), ".photokit-export.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			if !exportLockWouldBlock(err) {
				_ = file.Close()
				return nil, err
			}
			select {
			case <-ctx.Done():
				_ = file.Close()
				return nil, ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
			continue
		}
		return &exportLock{file: file}, nil
	}
}

func exportLockWouldBlock(err error) bool {
	errno, ok := err.(syscall.Errno)
	return ok && (errno == syscall.EWOULDBLOCK || errno == syscall.EAGAIN)
}

func (l *exportLock) Close() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

func RenderCanonicalJPEG(ctx context.Context, sourcePath, destinationPath string, quality float64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	cSource := C.CString(sourcePath)
	defer C.free(unsafe.Pointer(cSource))
	cDestination := C.CString(destinationPath)
	defer C.free(unsafe.Pointer(cDestination))

	var cErr *C.char
	ok := C.photoscrawl_render_canonical_jpeg(cSource, cDestination, C.double(quality), &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return errors.New(C.GoString(cErr))
	}
	if ok == 0 {
		return errors.New("render canonical JPEG failed")
	}
	return nil
}

func ImageMetadataRecord(ctx context.Context, sourcePath string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	cSource := C.CString(sourcePath)
	defer C.free(unsafe.Pointer(cSource))

	var cErr *C.char
	cJSON := C.photoscrawl_image_metadata_record_json(cSource, &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return nil, errors.New(C.GoString(cErr))
	}
	if cJSON == nil {
		return nil, errors.New("image metadata returned no JSON")
	}
	defer C.free(unsafe.Pointer(cJSON))

	return []byte(C.GoString(cJSON)), nil
}

func writeImageMetadataFixtureForTest(destinationPath string) error {
	cDestination := C.CString(destinationPath)
	defer C.free(unsafe.Pointer(cDestination))
	var cErr *C.char
	if C.photoscrawl_write_image_metadata_fixture(cDestination, &cErr) == 0 {
		if cErr != nil {
			defer C.free(unsafe.Pointer(cErr))
			return errors.New(C.GoString(cErr))
		}
		return errors.New("write synthetic ImageIO metadata fixture")
	}
	return nil
}

func imageMetadataTypedFixtureForTest() ([]byte, error) {
	var cErr *C.char
	cJSON := C.photoscrawl_image_metadata_typed_fixture_json(&cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return nil, errors.New(C.GoString(cErr))
	}
	if cJSON == nil {
		return nil, errors.New("typed ImageIO metadata fixture returned no JSON")
	}
	defer C.free(unsafe.Pointer(cJSON))
	return []byte(C.GoString(cJSON)), nil
}

// ImageMetadata keeps the research harness on the same complete extractor as
// production while it still accepts generic JSON.
func ImageMetadata(ctx context.Context, sourcePath string) (map[string]any, error) {
	record, err := ImageMetadataRecord(ctx, sourcePath)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(record, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func boolInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}
