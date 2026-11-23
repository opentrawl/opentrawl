package archivetime

import (
	"fmt"
	"strings"
	"time"
)

// FixedNanosecondUTCLayout formats UTC timestamps with a fixed 9-digit
// fractional second width so lexicographical order matches chronological order
// even across whole-second and subsecond timestamps.
const FixedNanosecondUTCLayout = "2006-01-02T15:04:05.000000000Z07:00"

// FormatRFC3339UTC formats a non-zero time in UTC using RFC3339 second precision.
func FormatRFC3339UTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// FormatRFC3339NanoUTC formats a non-zero time in UTC using RFC3339Nano precision.
func FormatRFC3339NanoUTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// FormatFixedNanosecondRFC3339UTC formats a non-zero time in UTC with fixed
// 9-digit fractional seconds so stored strings sort chronologically.
func FormatFixedNanosecondRFC3339UTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(FixedNanosecondUTCLayout)
}

// ParseRFC3339UTC parses an RFC3339 or RFC3339Nano timestamp string into UTC.
func ParseRFC3339UTC(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("parse archive time: empty value")
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse archive time %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

// ParseOptionalRFC3339UTC parses an RFC3339 or RFC3339Nano timestamp into UTC,
// returning the zero time when the input is blank or unparseable.
func ParseOptionalRFC3339UTC(value string) time.Time {
	parsed, err := ParseRFC3339UTC(value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// ParseRFC3339UnixSeconds parses an RFC3339 or RFC3339Nano timestamp string
// into UTC Unix seconds.
func ParseRFC3339UnixSeconds(value string) (int64, bool) {
	parsed, err := ParseRFC3339UTC(value)
	if err != nil || parsed.IsZero() {
		return 0, false
	}
	return parsed.Unix(), true
}
