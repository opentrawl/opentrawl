package flags

import (
	"errors"
	"strings"
	"time"
)

// ErrDateFormat is returned when a value matches none of the fleet's three
// accepted date layouts. Callers wrap it in their own usage-error type, the
// same pattern as ErrLimitBelowOne.
var ErrDateFormat = errors.New("must be RFC3339 or YYYY-MM-DD")

// Date parses an --after/--before style flag value under the one fleet date
// grammar shared by the Twitter, Gmail and Messages crawlers:
// an RFC3339Nano or RFC3339 timestamp, or a bare YYYY-MM-DD date. A bare date
// is read as midnight in the machine's local timezone — a person typing
// "2026-07-04" means their own day, not UTC's — and the returned time is
// always UTC.
func Date(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, ErrDateFormat
}
