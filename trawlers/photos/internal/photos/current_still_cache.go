package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
)

const currentStillCacheKeyVersion = "photos-current-still-v2"

// CurrentStillCachePath includes the source identity, asset UUID and canonical
// microsecond modification instant. An edit at that precision cannot reuse
// earlier visual bytes.
func CurrentStillCachePath(root, sourceLibraryID, assetUUID string, modification CurrentStillModification) string {
	key := strings.Join([]string{
		currentStillCacheKeyVersion,
		"current-still",
		sourceLibraryID,
		strings.ToLower(strings.TrimSpace(assetUUID)),
		strconv.FormatInt(modification.UnixSeconds, 10),
		strconv.FormatInt(int64(modification.Microseconds), 10),
	}, "\x00")
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(root, hex.EncodeToString(digest[:])+".current")
}
