package attachfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyRejectsUnsafeAttachmentID(t *testing.T) {
	src := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "/", "..", "../escape", "a/b", "a" + string(os.PathSeparator) + "b"} {
		if _, _, err := Copy(t.TempDir(), id, src); err == nil {
			t.Fatalf("Copy accepted unsafe attachment id %q", id)
		}
	}
}

func TestLocateRefusesMultipleFiles(t *testing.T) {
	container := t.TempDir()
	dir := filepath.Join(container, "Accounts", "acct", "Media", "media-1", "gen")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.jpg", "two.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := Locate(container, "media-1")
	if err == nil {
		t.Fatal("Locate chose silently between two files")
	}
	if !strings.Contains(err.Error(), "one.jpg") || !strings.Contains(err.Error(), "two.jpg") {
		t.Fatalf("error does not name the candidates: %v", err)
	}
}
