//go:build !darwin

package apple

import "testing"

func TestReadSystemUnsupported(t *testing.T) {
	_, err := ReadSystem(t.Context())
	if err == nil {
		t.Fatal("expected unsupported error")
	}
}

func TestCheckSourceUnsupported(t *testing.T) {
	state, err := CheckSource(t.Context())
	if state != SourceUnavailable {
		t.Fatalf("state = %q, want %q", state, SourceUnavailable)
	}
	if err == nil {
		t.Fatal("expected unsupported error")
	}
}
