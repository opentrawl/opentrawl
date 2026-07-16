package trawlkit

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestReadVerbsNeverMutateArchive(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	archivePath := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	before := fileSHA256(t, archivePath)

	source := &testOpenContactCrawler{testContactCrawler: &testContactCrawler{testCrawler: &testCrawler{}}}

	commands := [][]string{
		{"metadata", "--json"},
		{"status", "--json"},
		{"search", "--json", "needle"},
		{"who", "--json", "Ada"},
		{"open", "--json", "testcrawl:1"},
	}
	for _, argv := range commands {
		code, stdout, stderr := runForTestAt(stateRoot, argv, source, runOptions{})
		if code != 0 {
			t.Fatalf("%v code=%d stdout=%s stderr=%s", argv, code, stdout, stderr)
		}
	}

	after := fileSHA256(t, archivePath)
	if before != after {
		t.Fatalf("read verb mutated archive: before=%x after=%x", before, after)
	}
	t.Logf("archive_hash_unchanged=true archive_path=%s hash=%x", archivePath, before)
}

func fileSHA256(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}
