package notesdb

import "testing"

func TestDefaultStorePathRequiresHome(t *testing.T) {
	t.Setenv("HOME", "")

	if path, err := DefaultStorePath(); err == nil {
		t.Fatalf("DefaultStorePath() = %q, nil error; want error", path)
	}
}
