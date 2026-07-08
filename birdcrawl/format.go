package birdcrawl

import (
	"strconv"

	"github.com/openclaw/crawlkit/render"
)

func itoa(value int) string {
	return strconv.Itoa(value)
}

// groupDigits renders 41303 as "41,303" so counts are readable aloud.
func groupDigits(value int) string {
	return groupDigits64(int64(value))
}

func groupDigits64(value int64) string {
	return render.FormatInteger(value)
}
