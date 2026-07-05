package archive

import (
	"testing"
	"time"
)

// TestSearchTimeBoundDateOnlyUsesLocalMidnight pins a deliberate behavior
// change from the TRAWL-131 date-parser lift: searchTimeBound used to read
// a bare YYYY-MM-DD date as UTC midnight (time.Parse); it now goes through
// crawlkit/flags.Date, which reads it as midnight in the machine's local
// timezone, matching every other crawler's --after/--before grammar. The
// zone is fixed here so the test does not depend on the machine it runs on.
func TestSearchTimeBoundDateOnlyUsesLocalMidnight(t *testing.T) {
	fixed := time.FixedZone("UTC+2", 2*60*60)
	previous := time.Local
	time.Local = fixed
	t.Cleanup(func() { time.Local = previous })

	got, err := searchTimeBound("2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-07-03T22:00:00Z"
	if got != want {
		t.Fatalf("searchTimeBound(\"2026-07-04\") = %q, want %q (local midnight in UTC+2, not UTC midnight)", got, want)
	}
}

func TestSearchTimeBoundEmptyAndError(t *testing.T) {
	if got, err := searchTimeBound(""); err != nil || got != "" {
		t.Fatalf("searchTimeBound(\"\") = (%q, %v), want (\"\", nil)", got, err)
	}
	if _, err := searchTimeBound("not-a-date"); err == nil {
		t.Fatal("searchTimeBound(\"not-a-date\") = nil error, want an error")
	}
}

// TestNormalizeKnownPlaceTimeDateOnlyUsesLocalMidnight pins the same
// deliberate behavior change for a known-place's valid_from/valid_until:
// a bare YYYY-MM-DD date now means local midnight, not UTC midnight,
// matching searchTimeBound and every crawler's --after/--before grammar.
// Deliberately not t.Parallel() -- it mutates the process-wide time.Local
// and must finish (with its cleanup) before any parallel test in this
// package resumes.
func TestNormalizeKnownPlaceTimeDateOnlyUsesLocalMidnight(t *testing.T) {
	fixed := time.FixedZone("UTC+2", 2*60*60)
	previous := time.Local
	time.Local = fixed
	t.Cleanup(func() { time.Local = previous })

	normalized, parsed, ok, err := normalizeKnownPlaceTime("2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("normalizeKnownPlaceTime(\"2026-07-04\") ok = false, want true")
	}
	want := "2026-07-03T22:00:00Z"
	if normalized != want {
		t.Fatalf("normalizeKnownPlaceTime(\"2026-07-04\") = %q, want %q (local midnight in UTC+2, not UTC midnight)", normalized, want)
	}
	if wantTime, _ := time.Parse(time.RFC3339, want); !parsed.Equal(wantTime) {
		t.Fatalf("normalizeKnownPlaceTime(\"2026-07-04\") time = %v, want %v", parsed, wantTime)
	}
}
