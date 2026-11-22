//go:build !darwin

package render

import (
	"strings"

	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func FormatExactMonetaryAmount(amount *presentation.ExactMonetaryAmount) string {
	if amount == nil {
		return ""
	}
	decimal := amount.GetExactDecimalAmount()
	signed := amount.GetMonetaryAmountDisplaySignStyle() == presentation.MonetaryAmountDisplaySignStyle_MONETARY_AMOUNT_DISPLAY_SIGN_STYLE_SIGNED_MOVEMENT
	if strings.HasPrefix(decimal, "-") {
		decimal = "−" + strings.TrimPrefix(decimal, "-")
	} else if signed && strings.Trim(decimal, "0.") != "" {
		decimal = "+" + decimal
	}
	return amount.GetIso_4217CurrencyCode() + " " + decimal
}
