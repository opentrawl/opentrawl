//go:build darwin

package photos

/*
#cgo darwin LDFLAGS: -framework Foundation -framework Photos -framework CoreLocation -framework CoreImage -framework CoreGraphics -framework ImageIO
#include <stdlib.h>

int photoscrawl_export_original_resource(const char *localIdentifier, const char *destinationPath, int allowNetwork, char **errorOut);
int photoscrawl_render_canonical_jpeg(const char *sourcePath, const char *destinationPath, double quality, char **errorOut);
char *photoscrawl_image_metadata_json(const char *sourcePath, char **errorOut);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func ExportOriginalResource(ctx context.Context, localIdentifier, destinationPath string, allowNetwork bool) error {
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
	defer os.Remove(tempDestination)

	cIdentifier := C.CString(localIdentifier)
	defer C.free(unsafe.Pointer(cIdentifier))
	cDestination := C.CString(tempDestination)
	defer C.free(unsafe.Pointer(cDestination))

	var cErr *C.char
	ok := C.photoscrawl_export_original_resource(cIdentifier, cDestination, boolInt(allowNetwork), &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
		return errors.New(C.GoString(cErr))
	}
	if ok == 0 {
		return errors.New("export original resource failed")
	}
	if err := os.Rename(tempDestination, destinationPath); err != nil {
		return err
	}
	return nil
}

type exportLock struct {
	file *os.File
}

func acquireExportLock(destinationPath string) (*exportLock, error) {
	lockPath := filepath.Join(filepath.Dir(destinationPath), ".photokit-export.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errno, ok := err.(syscall.Errno); ok && (errno == syscall.EWOULDBLOCK || errno == syscall.EAGAIN) {
			return nil, ErrExportAlreadyRunning
		}
		return nil, err
	}
	return &exportLock{file: file}, nil
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
