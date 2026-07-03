package archive

import (
	"strconv"
	"strings"
)

func NormalizeEventStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return ""
	case "confirmed", "unknown":
		return value
	case "tentative":
		return "tentative"
	case "cancelled", "canceled":
		return "cancelled"
	}
	if strings.HasPrefix(value, "status_") {
		return eventStatusNumber(strings.TrimPrefix(value, "status_"))
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return eventStatusNumber(value)
	}
	return "unknown"
}

func eventStatusNumber(value string) string {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return "unknown"
	}
	switch number {
	case 0, 1:
		return "confirmed"
	case 2:
		return "tentative"
	case 3:
		return "cancelled"
	default:
		return "unknown"
	}
}
