package photoscrawl

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c Config) Validate() error {
	if c.CardModel.configured() {
		return c.CardModel.validate()
	}
	return nil
}

func validEnvironmentName(value string) bool {
	for index, r := range strings.TrimSpace(value) {
		if index == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return strings.TrimSpace(value) != ""
}

func configError(field, fix, message string) error {
	return trawlkit.ConfigFieldError{
		Field: field,
		Fix:   fix,
		Err:   fmt.Errorf("%s", message),
	}
}
