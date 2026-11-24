// Package picturecache keeps fetched pictures in a folder with a byte cap. When
// the folder is over its cap, the oldest pictures are deleted; a step that
// needs a deleted picture fetches it again.
package picturecache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// PictureCacheByteCapacity holds about 1,500 to 2,500 pictures of 2576 pixels.
const PictureCacheByteCapacity = 2 << 30

type PictureCache struct {
	pictureCacheFolderPath string
	pictureCacheByteCap    int64
	mu                     sync.Mutex
	cachedByteCountKnown   bool
	cachedByteCount        int64
}

// PictureCacheFolderPath is the cache folder beside a Photos archive.
func PictureCacheFolderPath(photosArchivePath string) string {
	return filepath.Join(filepath.Dir(photosArchivePath), "picture-cache")
}

func NewPictureCache(pictureCacheFolderPath string) *PictureCache {
	return &PictureCache{pictureCacheFolderPath: pictureCacheFolderPath, pictureCacheByteCap: PictureCacheByteCapacity}
}

func (pictureCache *PictureCache) cachedPictureFilePath(pictureSHA256 string) string {
	return filepath.Join(pictureCache.pictureCacheFolderPath, pictureSHA256+".jpg")
}

// CachedPictureFilePath returns false when the picture is not in the cache.
func (pictureCache *PictureCache) CachedPictureFilePath(pictureSHA256 string) (string, bool) {
	if pictureSHA256 == "" {
		return "", false
	}
	cachedPicturePath := pictureCache.cachedPictureFilePath(pictureSHA256)
	if _, err := os.Stat(cachedPicturePath); err != nil {
		return "", false
	}
	return cachedPicturePath, true
}

// DeletePicture removes one cached JPEG if present.
func (pictureCache *PictureCache) DeletePicture(pictureSHA256 string) error {
	if pictureSHA256 == "" {
		return nil
	}
	pictureCache.mu.Lock()
	defer pictureCache.mu.Unlock()
	cachedPath := pictureCache.cachedPictureFilePath(pictureSHA256)
	var removedBytes int64
	if info, err := os.Stat(cachedPath); err == nil {
		removedBytes = info.Size()
	}
	if err := os.Remove(cachedPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete cached picture: %w", err)
	}
	if pictureCache.cachedByteCountKnown && removedBytes > 0 {
		pictureCache.cachedByteCount = max(int64(0), pictureCache.cachedByteCount-removedBytes)
	}
	return nil
}

// StorePicture writes the JPEG under its SHA-256, then deletes the oldest
// pictures only when the cache exceeds its byte cap.
func (pictureCache *PictureCache) StorePicture(pictureSHA256 string, pictureJPEGBytes []byte) error {
	pictureCache.mu.Lock()
	defer pictureCache.mu.Unlock()
	if err := os.MkdirAll(pictureCache.pictureCacheFolderPath, 0o700); err != nil {
		return fmt.Errorf("create picture cache: %w", err)
	}
	targetPath := pictureCache.cachedPictureFilePath(pictureSHA256)
	var existingBytes int64
	if info, err := os.Stat(targetPath); err == nil {
		existingBytes = info.Size()
	}
	partialFile, err := os.CreateTemp(pictureCache.pictureCacheFolderPath, ".partial-*")
	if err != nil {
		return fmt.Errorf("write picture to cache: %w", err)
	}
	_, writeErr := partialFile.Write(pictureJPEGBytes)
	closeErr := partialFile.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(partialFile.Name())
		return fmt.Errorf("write picture to cache: %w", err)
	}
	if err := os.Rename(partialFile.Name(), targetPath); err != nil {
		_ = os.Remove(partialFile.Name())
		return fmt.Errorf("write picture to cache: %w", err)
	}
	if !pictureCache.cachedByteCountKnown {
		return pictureCache.deleteOldestPicturesOverCap(pictureSHA256 + ".jpg")
	}
	pictureCache.cachedByteCount += int64(len(pictureJPEGBytes)) - existingBytes
	if pictureCache.cachedByteCount <= pictureCache.pictureCacheByteCap {
		return nil
	}
	return pictureCache.deleteOldestPicturesOverCap(pictureSHA256 + ".jpg")
}

// A partial file left by a crash is deleted like any other old file.
func (pictureCache *PictureCache) deleteOldestPicturesOverCap(newestPictureFileName string) error {
	cacheEntries, err := os.ReadDir(pictureCache.pictureCacheFolderPath)
	if err != nil {
		return fmt.Errorf("read picture cache: %w", err)
	}
	cachedFiles := make([]fs.FileInfo, 0, len(cacheEntries))
	var cachedByteCount int64
	for _, cacheEntry := range cacheEntries {
		cachedFile, err := cacheEntry.Info()
		if err != nil {
			continue
		}
		cachedFiles = append(cachedFiles, cachedFile)
		cachedByteCount += cachedFile.Size()
	}
	if cachedByteCount > pictureCache.pictureCacheByteCap {
		sort.Slice(cachedFiles, func(left, right int) bool {
			return cachedFiles[left].ModTime().Before(cachedFiles[right].ModTime())
		})
		for _, cachedFile := range cachedFiles {
			if cachedByteCount <= pictureCache.pictureCacheByteCap {
				break
			}
			if cachedFile.Name() == newestPictureFileName {
				continue
			}
			if err := os.Remove(filepath.Join(pictureCache.pictureCacheFolderPath, cachedFile.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("delete old picture from cache: %w", err)
			}
			cachedByteCount -= cachedFile.Size()
		}
	}
	pictureCache.cachedByteCount = cachedByteCount
	pictureCache.cachedByteCountKnown = true
	return nil
}
