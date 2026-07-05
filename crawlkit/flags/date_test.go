package flags

import (
	"errors"
	"testing"
	"time"
)

func TestDate(t *testing.T) {
	// Fixed to a real, non-UTC, non-caller-timezone offset so the
	// date-only case's local-midnight behavior is pinned regardless of
	// what timezone the test happens to run in (a UTC CI worker would
	// not have caught a regression back to UTC-midnight parsing).
	fixed := time.FixedZone("UTC+2", 2*60*60)
	previous := time.Local
	time.Local = fixed
	t.Cleanup(func() { time.Local = previous })

	for _, tc := range []struct {
		name    string
		value   string
		want    time.Time
		wantErr error
	}{
		{name: "RFC3339Nano", value: "2026-07-04T12:00:00.5Z", want: time.Date(2026, 7, 4, 12, 0, 0, 500000000, time.UTC)},
		{name: "RFC3339", value: "2026-07-04T12:00:00Z", want: time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)},
		{name: "date only is midnight in the local zone, not UTC", value: "2026-07-04", want: time.Date(2026, 7, 4, 0, 0, 0, 0, fixed).UTC()},
		{name: "surrounding whitespace", value: "  2026-07-04  ", want: time.Date(2026, 7, 4, 0, 0, 0, 0, fixed).UTC()},
		{name: "empty is a format error", value: "", wantErr: ErrDateFormat},
		{name: "garbage is a format error", value: "yesterday", wantErr: ErrDateFormat},
		{name: "US-style date is not accepted", value: "07/04/2026", wantErr: ErrDateFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Date(tc.value)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Date(%q) err = %v, want %v", tc.value, err, tc.wantErr)
			}
			if err == nil && !got.Equal(tc.want) {
				t.Fatalf("Date(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}

	// A date-only value must actually shift by the zone offset, not just
	// happen to equal UTC midnight — the case above alone cannot tell
	// "parsed as local" apart from "parsed as UTC" if local ever is UTC.
	got, err := Date("2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	wantUTCMidnightShiftedBack := time.Date(2026, 7, 3, 22, 0, 0, 0, time.UTC)
	if !got.Equal(wantUTCMidnightShiftedBack) {
		t.Fatalf("Date(\"2026-07-04\") = %v, want %v (local midnight in UTC+2, not UTC midnight)", got, wantUTCMidnightShiftedBack)
	}
}
