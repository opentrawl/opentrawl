package whatsapp

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlkit/flags"
)

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, errors.New("empty time")
	}
	t, err := flags.Date(value)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
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
