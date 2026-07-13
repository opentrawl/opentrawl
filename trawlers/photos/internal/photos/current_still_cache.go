package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
)

const currentStillCacheKeyVersion = "photos-current-still-v2"

// CurrentStillCachePath includes the source identity, asset UUID and one
// canonical freshness token. Modification keys retain their existing form.
func CurrentStillCachePath(root, sourceLibraryID, assetUUID string, freshness CurrentStillFreshness) string {
	parts := []string{currentStillCacheKeyVersion, "current-still", sourceLibraryID, strings.ToLower(strings.TrimSpace(assetUUID))}
	if modification, ok := freshness.ExpectedModification(); ok {
		parts = append(parts, strconv.FormatInt(modification.UnixSeconds, 10), strconv.FormatInt(int64(modification.Microseconds), 10))
	} else {
		parts = append(parts, "source-fingerprint", freshness.sourceFingerprint)
	}
	key := strings.Join(parts, "\x00")
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(root, hex.EncodeToString(digest[:])+".current")
}
