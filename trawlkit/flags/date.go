package flags

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// ErrDateFormat is returned when a value matches none of the fleet's accepted
// date layouts. Callers wrap it in their own usage-error type, the
// same pattern as ErrLimitBelowOne.
var ErrDateFormat = output.HumanFacingErrorMessage("needs a time like 2026-07-04 12:00, 2026-07-04, or 2026-07-04T12:00:00Z.")

// Date parses an --after/--before style flag value under the one fleet date
// grammar shared by the Twitter, Gmail and Messages trawlers:
// an RFC3339Nano or RFC3339 timestamp, a local timestamp displayed with minute
// or second precision, or a bare YYYY-MM-DD date. Values without a timezone
// are read in the machine's local timezone — a person typing "2026-07-04"
// means their own day, not UTC's — and the returned time is always UTC.
func Date(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, ErrDateFormat
}

// ParseDateOrTimeThroughEndOfEnteredPrecision includes the complete local day,
// minute or second named by value. RFC3339 timestamps remain exact.
func ParseDateOrTimeThroughEndOfEnteredPrecision(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	parsedTime, err := Date(value)
	if err != nil {
		return time.Time{}, err
	}
	if _, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local); err == nil {
		return parsedTime.Add(time.Second - time.Nanosecond), nil
	}
	if _, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local); err == nil {
		return parsedTime.Add(time.Minute - time.Nanosecond), nil
	}
	if _, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return parsedTime.In(time.Local).AddDate(0, 0, 1).Add(-time.Nanosecond).UTC(), nil
	}
	return parsedTime, nil
}
