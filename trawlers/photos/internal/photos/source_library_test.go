package photos

import "testing"

func TestSourceLibraryIDPreservesArchiveIdentity(t *testing.T) {
	const libraryPath = "/synthetic/Fixture Photos Library.photoslibrary"
	const want = "source_library:030719d2ea61e2639a5a8c2009970d4d"
	if got := SourceLibraryID(libraryPath); got != want {
		t.Fatalf("SourceLibraryID(%q) = %q, want %q", libraryPath, got, want)
	}
}
