package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseTimeFlag parses an --after/--before value using the fleet date grammar
// (gogcrawl, calcrawl, imsgcrawl): an RFC3339 timestamp or a bare YYYY-MM-DD
// date, dates read in local time.
func parseTimeFlag(flagName, value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s requires a time", flagName)
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			utc := t.UTC()
			return &utc, nil
		}
	}
	return nil, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD: %s", flagName, value)
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

// formatLocalTime renders JSON timestamps in the machine's local offset;
// human output uses formatHumanLocalTime or render.ShortLocalTime.
func formatLocalTime(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}

func formatHumanLocalTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04")
}
