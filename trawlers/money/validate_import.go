package money

import (
	"fmt"
	"regexp"

	contract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
)

var exactMoneyDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var moneyCurrencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func validateMoneyImport(imported *contract.MoneyImport) error {
	for _, account := range imported.GetMoneyAccountImports() {
		amounts := []*contract.ExactMonetaryAmount{}
		for _, balance := range account.GetMoneyAccount().GetMoneyAccountBalances() {
			amounts = append(amounts, balance.GetAccountBalanceAmount())
		}
		for _, movement := range account.GetBookedMovementList().GetBookedMovements() {
			if movement.GetBookingDate() == nil && movement.GetChargeDate() == nil {
				return fmt.Errorf("booked movement requires a booking or charge date")
			}
			if movement.GetSignedAmountIntoMoneyAccount() == nil {
				return fmt.Errorf("booked movement requires a signed amount")
			}
			amounts = append(amounts, movement.SignedAmountIntoMoneyAccount, movement.AccountBalanceAfterMovement, movement.OriginalForeignCurrencyAmount, movement.ForeignCurrencyCommission)
		}
		for _, movement := range account.GetCompletePendingMovementList().GetPendingMovements() {
			if movement.GetChargeDate() == nil || movement.GetSignedAmountIntoMoneyAccount() == nil {
				return fmt.Errorf("pending movement requires charge date and signed amount")
			}
			amounts = append(amounts, movement.SignedAmountIntoMoneyAccount)
		}
		if billing := account.GetMoneyAccount().GetMoneyCardBilling(); billing != nil {
			amounts = append(amounts, billing.CurrentAmountOwed, billing.PendingChargeTotal, billing.LastStatementAmount, billing.LastStatementRemainingAmount, billing.PaymentDueAmount)
		}
		for _, amount := range amounts {
			if amount != nil && (!exactMoneyDecimal.MatchString(amount.ExactDecimalAmount) || !moneyCurrencyCode.MatchString(amount.GetIso_4217CurrencyCode())) {
				return fmt.Errorf("money amount requires an exact decimal and ISO currency code")
			}
		}
	}
	return nil
}
