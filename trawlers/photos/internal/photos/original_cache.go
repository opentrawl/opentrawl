package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

const originalCacheKeyVersion = "photos-camera-original-v3"

func OriginalCachePath(root, sourceLibraryID, modificationDate string, query OriginalExportQuery) string {
	identity := CanonicalAssetUUID(query.LocalIdentifier)
	if identity == "" {
		identity = query.LocalIdentifier
	}
	key := strings.Join([]string{
		originalCacheKeyVersion,
		sourceLibraryID,
		identity,
		modificationDate,
		query.OriginalFilename,
		query.OriginalUTI,
	}, "\x00")
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(root, hex.EncodeToString(digest[:])+OriginalExtension(query))
}

func OriginalExtension(query OriginalExportQuery) string {
	if extension := strings.ToLower(filepath.Ext(query.OriginalFilename)); IsOriginalExtension(extension) {
		return extension
	}
	switch strings.ToLower(query.OriginalUTI) {
	case "public.heic", "public.heif":
		return ".heic"
	case "public.jpeg", "public.jpg":
		return ".jpg"
	case "public.png":
		return ".png"
	case "public.tiff":
		return ".tiff"
	case "com.adobe.raw-image", "com.adobe.raw":
		return ".dng"
	default:
		return ".heic"
	}
}

func IsOriginalUTI(uti string) bool {
	switch strings.ToLower(uti) {
	case "public.heic", "public.heif", "public.jpeg", "public.jpg", "public.png", "public.tiff", "com.adobe.raw-image", "com.adobe.raw":
		return true
	default:
		return false
	}
}

func IsOriginalExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".heic", ".heif", ".jpg", ".jpeg", ".png", ".tif", ".tiff", ".dng":
		return true
	default:
		return false
	}
}
