package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalCachePathDoesNotReusePreviousResourceSemantics(t *testing.T) {
	query := OriginalExportQuery{
		LocalIdentifier:  "synthetic-asset",
		OriginalFilename: "synthetic.jpeg",
		OriginalUTI:      "public.jpeg",
	}
	const sourceLibraryID = "synthetic-library"
	const modificationDate = "2026-07-10T12:00:00Z"
	oldKey := strings.Join([]string{
		"photos-original-v2",
		sourceLibraryID,
		query.LocalIdentifier,
		modificationDate,
		query.OriginalFilename,
		query.OriginalUTI,
	}, "\x00")
	oldDigest := sha256.Sum256([]byte(oldKey))
	oldPath := filepath.Join("cache", hex.EncodeToString(oldDigest[:])+OriginalExtension(query))
	if got := OriginalCachePath("cache", sourceLibraryID, modificationDate, query); got == oldPath {
		t.Fatalf("camera-original cache reused previous resource semantics: %q", got)
	}
}

func TestOriginalCachePathKeysExactSourceVersion(t *testing.T) {
	query := OriginalExportQuery{
		LocalIdentifier:  "synthetic-asset",
		OriginalFilename: "synthetic.jpeg",
		OriginalUTI:      "public.jpeg",
	}
	first := OriginalCachePath("cache", "synthetic-library", "2026-07-10T12:00:00Z", query)
	second := OriginalCachePath("cache", "synthetic-library", "2026-07-10T12:00:01Z", query)
	if first == second {
		t.Fatalf("cache path did not change with the source version: %q", first)
	}
	if filepath.Ext(first) != ".jpeg" {
		t.Fatalf("cache extension = %q", filepath.Ext(first))
	}
}

func TestOriginalCachePathSeparatesSourceLibraries(t *testing.T) {
	query := OriginalExportQuery{LocalIdentifier: "synthetic-asset", OriginalFilename: "synthetic.jpeg"}
	first := OriginalCachePath("cache", "first-library", "2026-07-10T12:00:00Z", query)
	second := OriginalCachePath("cache", "second-library", "2026-07-10T12:00:00Z", query)
	if first == second {
		t.Fatalf("cache path reused across source libraries: %q", first)
	}
}

func TestOriginalExtensionFallsBackToUTI(t *testing.T) {
	if got := OriginalExtension(OriginalExportQuery{OriginalUTI: "com.adobe.raw-image"}); got != ".dng" {
		t.Fatalf("extension = %q, want .dng", got)
	}
}
