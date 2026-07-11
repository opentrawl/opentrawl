package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

const currentStillCacheKeyVersion = "photos-current-still-v1"

// CurrentStillCachePath includes the source identity, asset UUID and complete
// source modification instant. A later edit cannot reuse earlier visual bytes.
func CurrentStillCachePath(root, sourceLibraryID, assetUUID, modificationDate string) string {
	key := strings.Join([]string{currentStillCacheKeyVersion, "current-still", sourceLibraryID, strings.ToLower(strings.TrimSpace(assetUUID)), modificationDate}, "\x00")
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(root, hex.EncodeToString(digest[:])+".current")
}
