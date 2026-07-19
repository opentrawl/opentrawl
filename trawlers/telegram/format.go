package telegram

import "github.com/opentrawl/opentrawl/trawlkit/render"

func groupDigits(value int) string {
	return groupDigits64(int64(value))
}

func groupDigits64(value int64) string {
	return render.FormatInteger(value)
}
