package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func stableID(kind string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	return strings.TrimSpace(kind) + ":" + hex.EncodeToString(hash.Sum(nil))[:32]
}
