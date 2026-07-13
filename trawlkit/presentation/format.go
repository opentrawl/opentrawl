// Package presentation formats source-owned values for generic human output.
package presentation

import (
	"fmt"
	"strings"
	"time"
)

const timeLayout = "2 January 2006 at 15:04:05.999999999 -07:00"

// Timestamp converts an RFC 3339 timestamp to a readable form without
// changing its represented instant or numeric offset.
func Timestamp(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", fmt.Errorf("invalid RFC3339 timestamp %q: %w", value, err)
	}
	return parsed.Format(timeLayout), nil
}

// MustTimestamp formats a value already accepted by ValidateTimestamps.
func MustTimestamp(value string) string {
	formatted, err := Timestamp(value)
	if err != nil {
		panic(err)
	}
	return formatted
}

// ValidateTimestamps rejects any present timestamp that cannot be formatted.
func ValidateTimestamps(values ...string) error {
	for _, value := range values {
		if _, err := Timestamp(value); err != nil {
			return err
		}
	}
	return nil
}

// Bytes converts a byte count to a compact binary file size.
func Bytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d bytes", value)
	}
	units := []string{"bytes", "KiB", "MiB", "GiB", "TiB", "PiB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}
