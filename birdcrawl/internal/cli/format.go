package cli

import (
	"strconv"
	"strings"
)

func itoa(value int) string {
	return strconv.Itoa(value)
}

func itoa64(value int64) string {
	return strconv.FormatInt(value, 10)
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// groupDigits renders 41303 as "41,303" so counts are readable aloud.
func groupDigits(value int) string {
	return groupDigits64(int64(value))
}

func groupDigits64(value int64) string {
	s := strconv.FormatInt(value, 10)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}
