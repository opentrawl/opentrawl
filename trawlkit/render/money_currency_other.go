//go:build !darwin

package render

import (
	"strings"

	money "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
)

func FormatMoneyAmount(amount *money.ExactMonetaryAmount, signed bool) string {
	if amount == nil {
		return ""
	}
	decimal := amount.GetExactDecimalAmount()
	if strings.HasPrefix(decimal, "-") {
		decimal = "−" + strings.TrimPrefix(decimal, "-")
	} else if signed && strings.Trim(decimal, "0.") != "" {
		decimal = "+" + decimal
	}
	return amount.GetIso_4217CurrencyCode() + " " + decimal
}
