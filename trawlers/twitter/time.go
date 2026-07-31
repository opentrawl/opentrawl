package twitter

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

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

// formatLocalTime renders timestamps in the machine's local offset.
func formatLocalTime(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}
