package telecrawl

import (
	"strconv"
	"strings"
)

func groupDigits(value int) string {
	return groupDigits64(int64(value))
}

func groupDigits64(value int64) string {
	s := strconv.FormatInt(value, 10)
	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if negative {
		return "-" + s
	}
	return s
}
