package wacrawl

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/openclaw/crawlkit/flags"
)

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, errors.New("empty time")
	}
	t, err := flags.Date(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q", value)
	}
	return t, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(time.Local).Format(time.RFC3339)
}

func parseFormattedTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func displayRef(fullRef, shortRef string) string {
	if strings.TrimSpace(shortRef) != "" {
		return shortRef
	}
	return fullRef
}

func looksLikePhone(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	digits := 0
	other := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case strings.ContainsRune(" +()-.", r):
		default:
			other++
		}
	}
	return digits >= 5 && other == 0
}
