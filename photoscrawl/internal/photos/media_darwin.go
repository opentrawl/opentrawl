//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework Foundation -framework Photos -framework CoreLocation -framework CoreImage -framework CoreGraphics -framework ImageIO
#include <stdlib.h>

int photoscrawl_export_original_resource(const char *localIdentifier, const char *destinationPath, int allowNetwork, char **errorOut);
int photoscrawl_export_original_resource_matching(const char *localIdentifier, const char *creationDate, long long width, long long height, const char *originalFilename, const char *destinationPath, int allowNetwork, char **errorOut);
int photoscrawl_render_canonical_jpeg(const char *sourcePath, const char *destinationPath, double quality, char **errorOut);
char *photoscrawl_image_metadata_json(const char *sourcePath, char **errorOut);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

func ExportOriginalResource(ctx context.Context, localIdentifier, destinationPath string, allowNetwork bool) error {
	return ExportOriginalResourceMatching(ctx, OriginalExportQuery{LocalIdentifier: localIdentifier}, destinationPath, allowNetwork)
}

func ExportOriginalResourceMatching(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	lock, err := acquireExportLock(destinationPath)
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
	ok := C.photoscrawl_export_original_resource_matching(cIdentifier, cCreationDate, C.longlong(query.Width), C.longlong(query.Height), cOriginalFilename, cDestination, boolInt(allowNetwork), &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return originalExportError(C.GoString(cErr))
	}
	if ok == 0 {
		return errors.New("export original resource failed")
	}
	if err := os.Rename(tempDestination, destinationPath); err != nil {
		return err
	}
	return nil
}

func originalExportError(message string) error {
	trimmed := strings.TrimSpace(message)
	if strings.Contains(strings.ToLower(trimmed), "photokit asset not found") {
		if trimmed == "" || strings.EqualFold(trimmed, ErrPhotoKitAssetNotFound.Error()) {
			return ErrPhotoKitAssetNotFound
		}
		return fmt.Errorf("%w: %s", ErrPhotoKitAssetNotFound, trimmed)
	}
	return errors.New(trimmed)
}

type exportLock struct {
	file *os.File
}

func acquireExportLock(destinationPath string) (*exportLock, error) {
	lockPath := filepath.Join(filepath.Dir(destinationPath), ".photokit-export.lock")
	removedStale := false
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = file.Close()
			if exportLockWouldBlock(err) {
				if !removedStale {
					removed, removeErr := removeDeadExportLock(lockPath)
					if removeErr != nil {
						return nil, removeErr
					}
					if removed {
						removedStale = true
						continue
					}
				}
				return nil, ErrExportAlreadyRunning
			}
			return nil, err
		}
		if err := writeExportLockOwner(file); err != nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
			return nil, err
		}
		return &exportLock{file: file}, nil
	}
}

func exportLockWouldBlock(err error) bool {
	errno, ok := err.(syscall.Errno)
	return ok && (errno == syscall.EWOULDBLOCK || errno == syscall.EAGAIN)
}

func writeExportLockOwner(file *os.File) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		return err
	}
	return file.Sync()
}

func removeDeadExportLock(lockPath string) (bool, error) {
	pid, ok, err := readExportLockOwner(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	if !ok || processAlive(pid) {
		return false, nil
	}
	currentPID, currentOK, err := readExportLockOwner(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	if !currentOK || currentPID != pid || processAlive(currentPID) {
		return false, nil
	}
	if err := os.Remove(lockPath); err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return true, nil
}

func readExportLockOwner(lockPath string) (int, bool, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, false, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 0, false, nil
	}
	pid, err := strconv.Atoi(text)
	if err != nil || pid <= 0 {
		return 0, false, nil
	}
	return pid, true, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
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

func ImageMetadata(ctx context.Context, sourcePath string) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	cSource := C.CString(sourcePath)
	defer C.free(unsafe.Pointer(cSource))

	var cErr *C.char
	cJSON := C.photoscrawl_image_metadata_json(cSource, &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return nil, errors.New(C.GoString(cErr))
	}
	if cJSON == nil {
		return nil, errors.New("image metadata returned no JSON")
	}
	defer C.free(unsafe.Pointer(cJSON))

	var out map[string]any
	if err := json.Unmarshal([]byte(C.GoString(cJSON)), &out); err != nil {
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
