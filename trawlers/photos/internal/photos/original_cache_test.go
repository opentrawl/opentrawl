package photos

import (
	"path/filepath"
	"testing"
)

func TestOriginalCachePathKeysExactSourceVersion(t *testing.T) {
	query := OriginalExportQuery{
		LocalIdentifier:  "synthetic-asset",
		OriginalFilename: "synthetic.jpeg",
		OriginalUTI:      "public.jpeg",
	}
	first := OriginalCachePath("cache", "2026-07-10T12:00:00Z", query)
	second := OriginalCachePath("cache", "2026-07-10T12:00:01Z", query)
	if first == second {
		t.Fatalf("cache path did not change with the source version: %q", first)
	}
	if filepath.Ext(first) != ".jpeg" {
		t.Fatalf("cache extension = %q", filepath.Ext(first))
	}
}

func TestOriginalExtensionFallsBackToUTI(t *testing.T) {
	if got := OriginalExtension(OriginalExportQuery{OriginalUTI: "com.adobe.raw-image"}); got != ".dng" {
		t.Fatalf("extension = %q, want .dng", got)
	}
}
