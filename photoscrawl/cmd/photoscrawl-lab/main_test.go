package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/photoscrawl/internal/archive"
)

func TestUsageMentionsLabVerbs(t *testing.T) {
	err := run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: photoscrawl-lab <place-context|eval-card|known-places>") {
		t.Fatalf("unexpected usage error: %v", err)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("gemma4:31b, gemini-flash-latest,")
	want := []string{"gemma4:31b", "gemini-flash-latest"}
	if len(got) != len(want) {
		t.Fatalf("splitList = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitList = %#v", got)
		}
	}
}

func TestReadKnownPlacesInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known-places.json")
	data := []byte(`[{
	  "label_kind": "home",
	  "display_name": "Example Residence",
	  "latitude": 52,
	  "longitude": 4,
	  "valid_from": "2026-01-01T00:00:00Z"
	}]`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	places, err := readKnownPlacesInput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].LabelKind != archive.KnownPlaceKindHome || places[0].DisplayName != "Example Residence" {
		t.Fatalf("known places input = %#v", places)
	}
}
