package place

import (
	"errors"
	"fmt"
	"strings"
)

// classifyBridgeError is the single place where the bridge's message strings
// become typed errors. Everything above this boundary uses errors.Is.
func classifyBridgeError(message string) error {
	switch {
	case strings.Contains(message, "MKErrorDomain error 3"):
		return fmt.Errorf("%w: %s", ErrProviderThrottled, message)
	case strings.Contains(message, "timed out"):
		return fmt.Errorf("%w: %s", ErrProviderTimeout, message)
	case strings.Contains(message, "no placemarks"), strings.Contains(message, "no map items"):
		return fmt.Errorf("%w: %s", ErrProviderNoResult, message)
	}
	return errors.New(message)
}
