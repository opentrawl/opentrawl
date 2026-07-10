package photos

import (
	"crypto/sha256"
	"encoding/hex"
)

// SourceLibraryID returns the archive-compatible identity for a canonical
// absolute Photos library path.
func SourceLibraryID(canonicalLibraryPath string) string {
	digest := sha256.Sum256(append([]byte(canonicalLibraryPath), 0))
	return "source_library:" + hex.EncodeToString(digest[:])[:32]
}
