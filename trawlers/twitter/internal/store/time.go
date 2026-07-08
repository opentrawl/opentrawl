package store

import "time"

const UnknownTimeRFC3339 = "0001-01-01T00:00:00Z"

func formatUTC(t time.Time) string {
	if t.IsZero() {
		return UnknownTimeRFC3339
	}
	return t.UTC().Format(time.RFC3339)
}

func parseStoredTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
