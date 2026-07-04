package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseRFC3339Flag(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	utc := t.UTC()
	return &utc, nil
}

func parseWindow(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 30 * 24 * time.Hour, nil
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid day window %q", value)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(value)
}

func formatDuration(value time.Duration) string {
	if value%(24*time.Hour) == 0 {
		return strconv.Itoa(int(value/(24*time.Hour))) + "d"
	}
	return value.String()
}

func freshnessSpread(oldest, newest time.Time) string {
	switch {
	case oldest.IsZero() && newest.IsZero():
		return "counts not fetched"
	case oldest.Equal(newest):
		return formatLocalTime(oldest)
	default:
		return formatLocalTime(oldest) + " to " + formatLocalTime(newest)
	}
}

// formatLocalTime renders display timestamps in the machine's local offset,
// matching the suite convention (telecrawl formatLocalTime); storage stays UTC.
func formatLocalTime(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}
