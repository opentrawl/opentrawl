package store

import (
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/archivetime"
)

const UnknownTimeRFC3339 = "0001-01-01T00:00:00Z"

func formatUTC(t time.Time) string {
	if formatted := archivetime.FormatRFC3339UTC(t); formatted != "" {
		return formatted
	}
	return UnknownTimeRFC3339
}

func parseStoredTime(value string) time.Time {
	return archivetime.ParseOptionalRFC3339UTC(value)
}
