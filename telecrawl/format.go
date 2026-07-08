package telecrawl

import "github.com/openclaw/crawlkit/render"

func groupDigits(value int) string {
	return groupDigits64(int64(value))
}

func groupDigits64(value int64) string {
	return render.FormatInteger(value)
}
