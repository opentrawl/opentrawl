package trawlkit

import (
	"fmt"
	"strings"
	"time"
)

// ValidateOptionalRFC3339Timestamps rejects any non-empty timestamp string
// that is not valid RFC 3339 / RFC 3339 Nano.
func ValidateOptionalRFC3339Timestamps(values ...string) error {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, trimmedValue); err != nil {
			return fmt.Errorf("invalid RFC3339 timestamp %q: %w", trimmedValue, err)
		}
	}
	return nil
}
