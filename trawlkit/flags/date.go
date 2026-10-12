package flags

import (
	"errors"
	"strings"
	"time"
)

// ErrDateFormat is returned when a value matches none of the fleet's accepted
// date layouts. Only the caller knows which option held the value, so it
// reports render.DateOptionNeedsTime for that option.
var ErrDateFormat = errors.New("value matches no accepted date or time layout")

// Date parses an --after/--before style flag value under the one fleet date
// grammar shared by the Twitter, Gmail and Messages trawlers:
// an RFC3339Nano or RFC3339 timestamp, a local timestamp displayed with minute
// or second precision, or a bare YYYY-MM-DD date. Values without a timezone
// are read in the machine's local timezone — a person typing "2026-07-04"
// means their own day, not UTC's — and the returned time is always UTC.
func Date(value string) (time.Time, error) {
	value = dateTextWithoutPrintedUTCPart(value)
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		// The forms trawl prints, so a time copied from output pastes back.
		"2006-01-02 15:04 MST",
		"2006-01-02 15:04 -07:00",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, ErrDateFormat
}

// ParseDateOrTimeThroughEndOfEnteredPrecision includes the complete local day,
// minute or second named by value. RFC3339 timestamps remain exact.
func ParseDateOrTimeThroughEndOfEnteredPrecision(value string) (time.Time, error) {
	value = dateTextWithoutPrintedUTCPart(value)
	parsedTime, err := Date(value)
	if err != nil {
		return time.Time{}, err
	}
	if _, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local); err == nil {
		return parsedTime.Add(time.Second - time.Nanosecond), nil
	}
	for _, minuteLayout := range []string{"2006-01-02 15:04", "2006-01-02 15:04 MST", "2006-01-02 15:04 -07:00"} {
		if _, err := time.ParseInLocation(minuteLayout, value, time.Local); err == nil {
			return parsedTime.Add(time.Minute - time.Nanosecond), nil
		}
	}
	if _, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return parsedTime.In(time.Local).AddDate(0, 0, 1).Add(-time.Nanosecond).UTC(), nil
	}
	return parsedTime, nil
}

// dateTextWithoutPrintedUTCPart drops the bracketed UTC part of a time trawl
// printed, "2026-10-11 11:05 +02:00 (09:05 UTC)", so it pastes back as its
// local time and offset.
func dateTextWithoutPrintedUTCPart(value string) string {
	localPart, _, _ := strings.Cut(value, " (")
	return strings.TrimSpace(localPart)
}
