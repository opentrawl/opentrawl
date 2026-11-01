// Money payment and account details.
package render

import (
	"fmt"
	"strings"

	money "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/proto"
)

func moneyMethodText(m *money.MoneyMovement) string {
	text := map[money.MoneyPaymentMethod]string{money.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_ONLINE_BANKING: "online banking", money.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_APPLE_PAY: "Apple Pay", money.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_CARD: "card", money.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_DIRECT_DEBIT: "direct debit", money.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_CASH_WITHDRAWAL: "cash withdrawal"}[m.GetPaymentMethod()]
	if m.GetPaymentCardLastFourDigits() != "" {
		text += " (card " + m.GetPaymentCardLastFourDigits() + ")"
	}
	return text
}
func groupMoneyAccountIdentifier(s string) string {
	s = strings.ReplaceAll(s, " ", "")
	var parts []string
	for len(s) > 4 {
		parts = append(parts, s[:4])
		s = s[4:]
	}
	parts = append(parts, s)
	return strings.Join(parts, " ")
}
func (o TrawlCommandOutput) WriteMoneyFullDetail(r *money.OpenedMoneyMovement, links map[string]string) error {
	m := r.GetMoneyMovement()
	a := r.GetMoneyAccount()
	title := m.GetCounterpartyDisplayName()
	if m.GetMerchantCity() != "" {
		title += ", " + m.GetMerchantCity()
	}
	if m.GetMerchantCountryName() != "" {
		title += ", " + m.GetMerchantCountryName()
	}
	if title != "" {
		if err := writeLines(o.standardOutput, Wrap(title, OutputWidth(o.standardOutput))); err != nil {
			return err
		}
	}
	preposition := "from"
	if m.GetChargeDate() != nil {
		preposition = "on"
	} else if !strings.HasPrefix(m.GetSignedAmountIntoMoneyAccount().GetExactDecimalAmount(), "-") {
		preposition = "to"
	}
	if err := writeLine(o.standardOutput, FormatMoneyAmount(m.GetSignedAmountIntoMoneyAccount(), true)+" "+preposition+" "+moneyAccountName(a)); err != nil {
		return err
	}
	var fields []cardField
	var paidDate *presentation.CalendarDate
	if clock := m.GetPaymentTime(); clock != nil {
		paidDate = clock.GetPaymentLocalDate()
		value := fmt.Sprintf("%s %02d:%02d", formatCalendarDate(paidDate), clock.GetPaymentLocalHour(), clock.GetPaymentLocalMinute())
		if method := moneyMethodText(m); method != "" {
			value += ", by " + method
		}
		fields = append(fields, cardField{"Paid", value})
	} else if m.GetChargeDate() != nil {
		paidDate = m.GetChargeDate()
		fields = append(fields, cardField{"Charged", formatCalendarDate(paidDate)})
	}
	if m.GetBookingDate() != nil && !proto.Equal(m.GetBookingDate(), paidDate) {
		fields = append(fields, cardField{"Booked", formatCalendarDate(m.GetBookingDate())})
	}
	if m.GetValueDate() != nil && !proto.Equal(m.GetValueDate(), paidDate) && !proto.Equal(m.GetValueDate(), m.GetBookingDate()) {
		fields = append(fields, cardField{"Value date", formatCalendarDate(m.GetValueDate())})
	}
	if m.GetOriginalForeignCurrencyAmount() != nil {
		value := FormatMoneyAmount(m.GetOriginalForeignCurrencyAmount(), false)
		if rate := m.GetCurrencyExchangeRate(); rate != nil {
			value += " at " + rate.GetQuoteCurrencyUnitsPerBaseCurrencyUnit() + " " + rate.GetQuoteCurrencyCode() + " per " + rate.GetBaseCurrencyCode()
		}
		if m.GetForeignCurrencyCommission() != nil {
			value += "; charge includes " + FormatMoneyAmount(m.GetForeignCurrencyCommission(), false) + " commission"
		}
		fields = append(fields, cardField{"Original", value})
	}
	if m.GetOriginalForeignCurrencyAmount() == nil && m.GetCurrencyExchangeRate() != nil {
		rate := m.GetCurrencyExchangeRate()
		fields = append(fields, cardField{"Exchange rate", rate.GetQuoteCurrencyUnitsPerBaseCurrencyUnit() + " " + rate.GetQuoteCurrencyCode() + " per " + rate.GetBaseCurrencyCode()})
	}
	if h := m.GetHotelStay(); h != nil {
		fields = append(fields, cardField{"Stay", formatCalendarDate(h.GetArrivalDate()) + " – " + formatCalendarDate(h.GetDepartureDate())})
	}
	if f := m.GetFlightTicket(); f != nil {
		fields = append(fields, cardField{"Flight", f.GetDeparturePlace() + " → " + f.GetArrivalPlace() + ", " + f.GetAirlineCode() + ", class " + f.GetBookingClassCode()}, cardField{"Passenger", f.GetPassengerName()}, cardField{"Ticket", f.GetTicketNumber()})
	}
	if m.GetCardStatementEndDate() != nil {
		fields = append(fields, cardField{"Statement closes", formatCalendarDate(m.GetCardStatementEndDate())})
	}
	if m.GetCounterpartyAccountIdentifier() != "" {
		label := "To"
		if !strings.HasPrefix(m.GetSignedAmountIntoMoneyAccount().GetExactDecimalAmount(), "-") {
			label = "From"
		}
		fields = append(fields, cardField{label, groupMoneyAccountIdentifier(m.GetCounterpartyAccountIdentifier())})
	}
	fields = append(fields, cardField{"Reference", m.GetPaymentReference()})
	for _, f := range fields {
		if f.value != "" {
			if err := writeWrappedField(o.standardOutput, f.label, f.value); err != nil {
				return err
			}
		}
	}
	if m.GetMovementDescription() != "" {
		if err := writeLine(o.standardOutput, ""); err != nil {
			return err
		}
		if err := writeLines(o.standardOutput, Wrap(m.GetMovementDescription(), OutputWidth(o.standardOutput))); err != nil {
			return err
		}
	}
	for _, detail := range m.GetAdditionalPaymentDetails() {
		if err := writeLines(o.standardOutput, Wrap(detail, OutputWidth(o.standardOutput))); err != nil {
			return err
		}
	}
	if err := writeLine(o.standardOutput, ""); err != nil {
		return err
	}
	if err := writeLines(o.standardOutput, Wrap("From "+r.GetProviderDisplayName()+", read "+formatExactTimeInThisComputerTimeZone(r.GetProviderReadTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord)+".", OutputWidth(o.standardOutput))); err != nil {
		return err
	}
	var nextCommands []NextCommand
	if m.GetCounterpartyDisplayName() != "" {
		nextCommands = append(nextCommands, nextCommandWithLabel(nextCommandLabelSearchTrawlerArchive, "search", m.GetCounterpartyDisplayName()))
	}
	nextCommands = append(nextCommands, nextCommandWithLabel(nextCommandLabelMoreRows, "money", "transactions", "--account", links[a.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference()]))
	return writeNextCommands(o.standardOutput, nextCommands)
}
func (o TrawlCommandOutput) WriteMoneyCardAccountDetail(a *money.MoneyAccount, links map[string]string) error {
	b := a.GetMoneyCardBilling()
	if err := writeLine(o.standardOutput, a.GetMoneyAccountDisplayName()); err != nil {
		return err
	}
	fields := []cardField{{"Owed now", FormatMoneyAmount(b.GetCurrentAmountOwed(), false) + ", charges since the statement of " + formatCalendarDate(b.GetLastStatementDate())}, {"Pending", FormatMoneyAmount(b.GetPendingChargeTotal(), false)}}
	last := FormatMoneyAmount(b.GetLastStatementAmount(), false)
	if b.GetLastStatementRemainingAmount().GetExactDecimalAmount() == "0.00" || b.GetLastStatementRemainingAmount().GetExactDecimalAmount() == "0" {
		last += ", paid in full"
		if b.GetLastStatementPaidDate() != nil {
			last += " on " + formatCalendarDate(b.GetLastStatementPaidDate())
		}
	}
	fields = append(fields, cardField{"Last statement", last}, cardField{"Payment due", FormatMoneyAmount(b.GetPaymentDueAmount(), false)})
	for _, f := range fields {
		if err := writeWrappedField(o.standardOutput, f.label, f.value); err != nil {
			return err
		}
	}
	if err := writeLine(o.standardOutput, ""); err != nil {
		return err
	}
	if err := writeLines(o.standardOutput, Wrap("From "+a.GetMoneyAccountInstitutionDisplayName()+", read "+formatExactTimeInThisComputerTimeZone(b.GetBillingReadTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord)+".", OutputWidth(o.standardOutput))); err != nil {
		return err
	}
	return writeNextCommands(o.standardOutput, []NextCommand{nextCommandWithLabel(nextCommandLabelMoreRows, "money", "transactions", "--account", links[a.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference()])})
}
