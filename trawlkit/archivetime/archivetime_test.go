package archivetime

import (
	"testing"
	"time"
)

func TestParseAndFormatRFC3339UTC(t *testing.T) {
	whole, err := ParseRFC3339UTC("2026-08-14T00:00:00Z")
	if err != nil {
		t.Fatalf("parse whole second: %v", err)
	}
	subsec, err := ParseRFC3339UTC("2026-08-14T00:00:00.123456Z")
	if err != nil {
		t.Fatalf("parse subsecond: %v", err)
	}
	if !subsec.After(whole) {
		t.Fatalf("expected %v to be after %v", subsec, whole)
	}
	fixedWhole := FormatFixedNanosecondRFC3339UTC(whole)
	fixedSubsec := FormatFixedNanosecondRFC3339UTC(subsec)
	if fixedWhole >= fixedSubsec {
		t.Fatalf("expected fixed nanosecond strings to sort chronologically: %q vs %q", fixedWhole, fixedSubsec)
	}
	if unixSec, ok := ParseRFC3339UnixSeconds(" 2026-08-14T00:00:00.123Z "); !ok || unixSec != whole.Unix() {
		t.Fatalf("unexpected unix seconds: %d, %v", unixSec, ok)
	}
	if !ParseOptionalRFC3339UTC("not-a-timestamp").IsZero() {
		t.Fatalf("expected zero time for invalid input")
	}
}

func TestInclusiveTimeRangeContainsAndSQLClauses(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 1, 0, time.UTC)
	r := NewInclusiveTimeRange(start, end)

	if !r.ContainsTime(start) || !r.ContainsTime(end) {
		t.Fatalf("expected inclusive boundaries to match")
	}
	if !r.ContainsTime(start.Add(250 * time.Millisecond)) {
		t.Fatalf("expected subsecond interior timestamp to match")
	}
	if r.ContainsTime(start.Add(-time.Nanosecond)) || r.ContainsTime(end.Add(time.Nanosecond)) {
		t.Fatalf("expected out-of-range timestamps to be excluded")
	}

	var where []string
	var args []any
	r.AppendRFC3339UTCSQLClauses("capture_time_utc", &where, &args)
	if len(where) != 4 || len(args) != 4 {
		t.Fatalf("expected 4 coarse+exact RFC3339 SQL clauses, got %d clauses and %d args", len(where), len(args))
	}
}
