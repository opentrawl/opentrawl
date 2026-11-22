package money

import (
	"fmt"
	"strings"

	contract "github.com/opentrawl/opentrawl/trawlers/money/proto/trawl/money"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func presentationAmount(amount *contract.ExactMonetaryAmount, signed bool) *presentation.ExactMonetaryAmount {
	if amount == nil {
		return nil
	}
	style := presentation.MonetaryAmountDisplaySignStyle_MONETARY_AMOUNT_DISPLAY_SIGN_STYLE_BALANCE
	if signed {
		style = presentation.MonetaryAmountDisplaySignStyle_MONETARY_AMOUNT_DISPLAY_SIGN_STYLE_SIGNED_MOVEMENT
	}
	return &presentation.ExactMonetaryAmount{
		ExactDecimalAmount:             amount.GetExactDecimalAmount(),
		Iso_4217CurrencyCode:           amount.GetIso_4217CurrencyCode(),
		MonetaryAmountDisplaySignStyle: style,
	}
}

func textValue(value string) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: value},
	}
}

func timeValue(t *presentation.ArchiveRecordAssociatedTimeForDisplay) *presentation.TrawlerSpecificCommandPresentationValue {
	if t == nil {
		return textValue("")
	}
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTimeForDisplay: t,
		},
	}
}

func exactTimeValue(t *timestamppb.Timestamp) *presentation.TrawlerSpecificCommandPresentationValue {
	if t == nil || !t.IsValid() {
		return textValue("")
	}
	return timeValue(&presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: t},
	})
}

func calendarDateValue(date *presentation.CalendarDate) *presentation.TrawlerSpecificCommandPresentationValue {
	if date == nil {
		return textValue("")
	}
	return timeValue(&presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{CalendarDate: date},
	})
}

func refValue(ref *identity.CanonicalArchiveRecordReference) *presentation.TrawlerSpecificCommandPresentationValue {
	if ref == nil {
		return textValue("")
	}
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference{CanonicalRecordReference: ref},
	}
}

func amountValue(amount *contract.ExactMonetaryAmount, signed bool) *presentation.TrawlerSpecificCommandPresentationValue {
	if amount == nil {
		return textValue("")
	}
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ExactMonetaryAmount{
			ExactMonetaryAmount: presentationAmount(amount, signed),
		},
	}
}

func declarativeTextArg(text string) *presentation.DeclarativeTrawlCommandArgument {
	return &presentation.DeclarativeTrawlCommandArgument{
		ArgumentValue: &presentation.DeclarativeTrawlCommandArgument_TextArgument{TextArgument: text},
	}
}

func declarativeRefArg(ref *identity.CanonicalArchiveRecordReference) *presentation.DeclarativeTrawlCommandArgument {
	return &presentation.DeclarativeTrawlCommandArgument{
		ArgumentValue: &presentation.DeclarativeTrawlCommandArgument_CanonicalRecordReferenceArgument{CanonicalRecordReferenceArgument: ref},
	}
}

func moneyBalanceBasisLabel(balance *contract.MoneyBalance) string {
	return map[contract.MoneyBalanceBasis]string{
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_PORTFOLIO_VALUE:          "portfolio value",
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_CURRENT:             "owed now",
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_BOOKED:                   "booked",
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_AVAILABLE:                "available",
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_EXPECTED:                 "including pending",
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_STATEMENT:           "statement",
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_PREVIOUS_CARD_STATEMENT:  "previous statement",
		contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_REMAINING_CARD_STATEMENT: "remaining statement",
	}[balance.GetMoneyBalanceBasis()]
}

func balanceAmountForDisplay(balance *contract.MoneyBalance) *contract.ExactMonetaryAmount {
	amount := balance.GetAccountBalanceAmount()
	if balance.GetMoneyBalanceBasis() == contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_CURRENT && amount != nil {
		amount = proto.Clone(amount).(*contract.ExactMonetaryAmount)
		if strings.HasPrefix(amount.ExactDecimalAmount, "-") {
			amount.ExactDecimalAmount = strings.TrimPrefix(amount.ExactDecimalAmount, "-")
		} else if strings.Trim(amount.ExactDecimalAmount, "0.") != "" {
			amount.ExactDecimalAmount = "-" + amount.ExactDecimalAmount
		}
	}
	return amount
}

func latestMoneyBalancesByBasis(balances []*contract.MoneyBalance) []*contract.MoneyBalance {
	var latest []*contract.MoneyBalance
	for _, balance := range balances {
		found := false
		for index, previous := range latest {
			if previous.GetMoneyBalanceBasis() != balance.GetMoneyBalanceBasis() {
				continue
			}
			found = true
			if moneyBalanceDateText(balance) > moneyBalanceDateText(previous) {
				latest[index] = balance
			}
			break
		}
		if !found {
			latest = append(latest, balance)
		}
	}
	return latest
}

func moneyBalanceDateText(balance *contract.MoneyBalance) string {
	if date := balance.GetBalanceAsOf().GetCalendarDate(); date != nil {
		return fmt.Sprintf("%04d-%02d-%02d", date.GetCalendarYear(), date.GetCalendarMonthNumber(), date.GetCalendarDayOfMonth())
	}
	if balance.GetBalanceReadTime() == nil || !balance.GetBalanceReadTime().IsValid() {
		return ""
	}
	return balance.GetBalanceReadTime().AsTime().UTC().Format("2006-01-02T15:04:05.999999999Z")
}

func buildMoneyAccountsPresentation(accounts []*contract.MoneyAccount) *command.TrawlerCommandResponse {
	var balanceRows []*presentation.TrawlerSpecificCommandListPresentationRow
	var historyRows []*presentation.TrawlerSpecificCommandListPresentationRow
	for _, account := range accounts {
		balancesToShow := latestMoneyBalancesByBasis(account.GetMoneyAccountBalances())
		if account.GetMoneyCardBilling() != nil {
			for _, balance := range balancesToShow {
				if balance.GetMoneyBalanceBasis() == contract.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_CURRENT {
					balancesToShow = []*contract.MoneyBalance{balance}
					break
				}
			}
		}
		for _, balance := range balancesToShow {
			var observed *presentation.TrawlerSpecificCommandPresentationValue
			switch {
			case balance.GetBalanceAsOf() != nil:
				observed = timeValue(balance.GetBalanceAsOf())
			case balance.GetBalanceReadTime() != nil && balance.GetBalanceReadTime().IsValid():
				observed = exactTimeValue(balance.GetBalanceReadTime())
			default:
				observed = textValue("not recorded")
			}
			balanceRows = append(balanceRows, &presentation.TrawlerSpecificCommandListPresentationRow{
				ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
					textValue(account.GetMoneyAccountDisplayName()),
					amountValue(balanceAmountForDisplay(balance), false),
					textValue(moneyBalanceBasisLabel(balance)),
					observed,
					refValue(account.GetCanonicalRecordReference()),
				},
			})
		}
		if len(balancesToShow) == 0 {
			balanceRows = append(balanceRows, &presentation.TrawlerSpecificCommandListPresentationRow{
				ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
					textValue(account.GetMoneyAccountDisplayName()),
					textValue("unknown"),
					textValue("not read"),
					textValue(""),
					refValue(account.GetCanonicalRecordReference()),
				},
			})
		}
		for _, interval := range account.GetCompleteHistoryDateRanges() {
			dateBasis := "booked"
			if interval.GetMovementDateBasis() == contract.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_CHARGE_DATE {
				dateBasis = "charged"
			}
			historyRows = append(historyRows, &presentation.TrawlerSpecificCommandListPresentationRow{
				ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
					textValue(account.GetMoneyAccountDisplayName()),
					textValue(calendarDateText(interval.GetFirstCompleteDate()) + " – " + calendarDateText(interval.GetLastCompleteDate())),
					textValue(dateBasis),
					exactTimeValue(account.GetLastBookedMovementsReadTime()),
				},
			})
		}
		if len(account.GetCompleteHistoryDateRanges()) == 0 {
			historyRows = append(historyRows, &presentation.TrawlerSpecificCommandListPresentationRow{
				ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
					textValue(account.GetMoneyAccountDisplayName()),
					textValue("not established"),
					textValue(""),
					exactTimeValue(account.GetLastBookedMovementsReadTime()),
				},
			})
		}
	}
	type groupedNote struct {
		accountNames []string
		noteText     string
	}
	var groupedNotes []groupedNote
	groupIndexByNote := make(map[string]int)
	for _, account := range accounts {
		noteText := strings.TrimSpace(account.GetHistoryAvailabilityNote())
		if noteText == "" {
			continue
		}
		if existingIndex, seen := groupIndexByNote[noteText]; seen {
			groupedNotes[existingIndex].accountNames = append(groupedNotes[existingIndex].accountNames, account.GetMoneyAccountDisplayName())
			continue
		}
		groupIndexByNote[noteText] = len(groupedNotes)
		groupedNotes = append(groupedNotes, groupedNote{
			accountNames: []string{account.GetMoneyAccountDisplayName()},
			noteText:     noteText,
		})
	}
	footnotes := make([]string, 0, len(groupedNotes))
	for _, note := range groupedNotes {
		footnotes = append(footnotes, strings.Join(note.accountNames, ", ")+": "+note.noteText)
	}
	list := &presentation.TrawlerSpecificCommandListPresentation{
		ConciseTextShownWhenListIsEmpty: "No money accounts are archived yet.",
		FootnotesInDisplayOrder:         footnotes,
	}
	if len(balanceRows) > 0 || len(historyRows) > 0 {
		list.SectionsInDisplayOrder = []*presentation.TrawlerSpecificCommandListSection{
			{
				ColumnDisplayNamesInOrder: []string{"account", "balance", "basis", "as of / read", "link"},
				RowsInDisplayOrder:        balanceRows,
			},
			{
				ColumnDisplayNamesInOrder: []string{"account", "complete history", "basis", "last read"},
				RowsInDisplayOrder:        historyRows,
			},
		}
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
					TrawlerSpecificCommandListPresentation: list,
				},
			},
		},
	}
}

func buildMoneyTransactionsPresentation(
	accounts []*contract.MoneyAccount,
	matching []storedMovement,
	totalMatching int,
	limit int,
	basis string,
	pending bool,
) *command.TrawlerCommandResponse {
	kind := "Transactions"
	if pending {
		kind = "Pending"
	}
	dateHeading := "booked"
	if pending || basis == "charged" {
		dateHeading = "charged"
	}
	var sections []*presentation.TrawlerSpecificCommandListSection
	var firstRef *identity.CanonicalArchiveRecordReference
	hasChargeDateFallback := false
	for _, account := range accounts {
		accountRef := trawlkit.CanonicalArchiveRecordReferenceText(account.GetCanonicalRecordReference())
		var rows []*presentation.TrawlerSpecificCommandListPresentationRow
		for _, movement := range matching {
			if movement.accountReference != accountRef {
				continue
			}
			if pending {
				m := movement.pending
				if firstRef == nil {
					firstRef = m.GetCanonicalRecordReference()
				}
				rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
					ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
						calendarDateValue(m.GetChargeDate()),
						amountValue(m.GetSignedAmountIntoMoneyAccount(), true),
						textValue(m.GetCounterpartyDisplayName()),
						textValue(m.GetMovementDescription()),
						refValue(m.GetCanonicalRecordReference()),
					},
				})
				continue
			}
			m := movement.booked
			if firstRef == nil {
				firstRef = m.GetCanonicalRecordReference()
			}
			var dateCell *presentation.TrawlerSpecificCommandPresentationValue
			if basis == "charged" {
				dateCell = calendarDateValue(m.GetChargeDate())
			} else if m.GetBookingDate() != nil {
				dateCell = calendarDateValue(m.GetBookingDate())
			} else {
				dateCell = textValue(calendarDateText(m.GetChargeDate()) + "*")
				hasChargeDateFallback = true
			}
			rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
				ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
					dateCell,
					amountValue(m.GetSignedAmountIntoMoneyAccount(), true),
					textValue(m.GetCounterpartyDisplayName()),
					textValue(m.GetMovementDescription()),
					refValue(m.GetCanonicalRecordReference()),
				},
			})
		}
		if len(rows) > 0 {
			sections = append(sections, &presentation.TrawlerSpecificCommandListSection{
				SectionHeading:            account.GetMoneyAccountDisplayName(),
				ColumnDisplayNamesInOrder: []string{dateHeading, "amount", "counterparty", "description", "link"},
				RowsInDisplayOrder:        rows,
			})
		}
	}
	var footnotes []string
	if hasChargeDateFallback {
		footnotes = append(footnotes, "* Charge date; booking date not supplied.")
	}
	var nextCommands []*presentation.DeclarativeTrawlCommand
	if firstRef != nil {
		nextCommands = append(nextCommands, &presentation.DeclarativeTrawlCommand{
			CommandLabel: "Open",
			CommandArgumentsAfterTrawlInvocation: []*presentation.DeclarativeTrawlCommandArgument{
				declarativeTextArg("open"),
				declarativeRefArg(firstRef),
			},
		})
	}
	list := &presentation.TrawlerSpecificCommandListPresentation{
		ListSummaryHeading:         fmt.Sprintf("%s: showing %d of %s.", kind, len(matching), render.FormatInteger(int64(totalMatching))),
		SectionsInDisplayOrder:     sections,
		FootnotesInDisplayOrder:    footnotes,
		NextCommandsInDisplayOrder: nextCommands,
		TotalRowCount:              &presentation.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{ExactTotalRowCount: uint64(totalMatching)},
		MoreRowsExist:              totalMatching > limit,
		NextMaximumRowCount:        uint32(limit + 10),
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
					TrawlerSpecificCommandListPresentation: list,
				},
			},
		},
	}
}

func moneyMethodText(m *contract.MoneyMovement) string {
	text := map[contract.MoneyPaymentMethod]string{
		contract.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_ONLINE_BANKING:  "online banking",
		contract.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_APPLE_PAY:       "Apple Pay",
		contract.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_CARD:            "card",
		contract.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_DIRECT_DEBIT:    "direct debit",
		contract.MoneyPaymentMethod_MONEY_PAYMENT_METHOD_CASH_WITHDRAWAL: "cash withdrawal",
	}[m.GetPaymentMethod()]
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

func formatOpenedTimestamp(t *timestamppb.Timestamp) string {
	if t == nil || !t.IsValid() {
		return ""
	}
	local := t.AsTime().In(t.AsTime().Local().Location())
	utc := t.AsTime().UTC()
	utcText := utc.Format("15:04 UTC")
	if utc.Format("2006-01-02") != local.Format("2006-01-02") {
		utcText = utc.Format("2006-01-02 15:04 UTC")
	}
	return local.Format("2006-01-02 15:04 -07:00") + " (" + utcText + ")"
}

func buildOpenedMoneyAccountPresentation(account *contract.MoneyAccount) *open.TrawlerSpecificOpenedRecordPresentation {
	anchor := trawlkit.NewRecordAnchorIdentifier(openrecord.MoneyDetailAnchorID)
	nextCommands := []*presentation.DeclarativeTrawlCommand{{
		CommandLabel: "More",
		CommandArgumentsAfterTrawlInvocation: []*presentation.DeclarativeTrawlCommandArgument{
			declarativeTextArg("money"),
			declarativeTextArg("transactions"),
			declarativeTextArg("--account"),
			declarativeRefArg(account.GetCanonicalRecordReference()),
		},
	}}
	if b := account.GetMoneyCardBilling(); b != nil {
		last := render.FormatExactMonetaryAmount(presentationAmount(b.GetLastStatementAmount(), false))
		if b.GetLastStatementRemainingAmount().GetExactDecimalAmount() == "0.00" || b.GetLastStatementRemainingAmount().GetExactDecimalAmount() == "0" {
			last += ", paid in full"
			if b.GetLastStatementPaidDate() != nil {
				last += " on " + calendarDateText(b.GetLastStatementPaidDate())
			}
		}
		fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{
			{FieldDisplayName: "Owed now", FieldValue: textValue(render.FormatExactMonetaryAmount(presentationAmount(b.GetCurrentAmountOwed(), false)) + ", charges since the statement of " + calendarDateText(b.GetLastStatementDate()))},
			{FieldDisplayName: "Pending", FieldValue: amountValue(b.GetPendingChargeTotal(), false)},
			{FieldDisplayName: "Last statement", FieldValue: textValue(last)},
			{FieldDisplayName: "Payment due", FieldValue: amountValue(b.GetPaymentDueAmount(), false)},
		}
		footer := ""
		if b.GetBillingReadTime() != nil && b.GetBillingReadTime().IsValid() {
			footer = "From " + account.GetMoneyAccountInstitutionDisplayName() + ", read " + formatOpenedTimestamp(b.GetBillingReadTime()) + "."
		}
		return &open.TrawlerSpecificOpenedRecordPresentation{
			DetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
				DetailDisplayName:          account.GetMoneyAccountDisplayName(),
				DetailDisplayNameAnchor:    anchor,
				FieldsInDisplayOrder:       fields,
				DetailFooterText:           footer,
				NextCommandsInDisplayOrder: nextCommands,
			},
		}
	}
	var fields []*presentation.TrawlerSpecificCommandDetailPresentationField
	for _, balance := range account.GetMoneyAccountBalances() {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: moneyBalanceBasisLabel(balance),
			FieldValue:       amountValue(balanceAmountForDisplay(balance), false),
		})
		switch {
		case balance.GetBalanceAsOf() != nil:
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
				FieldDisplayName: "As of",
				FieldValue:       timeValue(balance.GetBalanceAsOf()),
			})
		case balance.GetBalanceReadTime() != nil && balance.GetBalanceReadTime().IsValid():
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
				FieldDisplayName: "Read",
				FieldValue:       exactTimeValue(balance.GetBalanceReadTime()),
			})
		default:
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
				FieldDisplayName: "Read",
				FieldValue:       textValue("not recorded"),
			})
		}
	}
	if len(account.GetMoneyAccountBalances()) == 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Balance",
			FieldValue:       textValue("unknown; not read"),
		})
	}
	if note := strings.TrimSpace(account.GetHistoryAvailabilityNote()); note != "" {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "History",
			FieldValue:       textValue(note),
		})
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{
		DetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
			DetailDisplayName:          account.GetMoneyAccountDisplayName(),
			DetailDisplayNameAnchor:    anchor,
			FieldsInDisplayOrder:       fields,
			NextCommandsInDisplayOrder: nextCommands,
		},
	}
}

func buildOpenedMoneyMovementPresentation(
	m *contract.MoneyMovement,
	account *contract.MoneyAccount,
	providerDisplayName string,
	providerReadTime *timestamppb.Timestamp,
) *open.TrawlerSpecificOpenedRecordPresentation {
	title := m.GetCounterpartyDisplayName()
	if m.GetMerchantCity() != "" {
		title += ", " + m.GetMerchantCity()
	}
	if m.GetMerchantCountryName() != "" {
		title += ", " + m.GetMerchantCountryName()
	}
	preposition := "from"
	if m.GetChargeDate() != nil {
		preposition = "on"
	} else if !strings.HasPrefix(m.GetSignedAmountIntoMoneyAccount().GetExactDecimalAmount(), "-") {
		preposition = "to"
	}
	subtitle := render.FormatExactMonetaryAmount(presentationAmount(m.GetSignedAmountIntoMoneyAccount(), true)) + " " + preposition + " " + account.GetMoneyAccountDisplayName()

	var fields []*presentation.TrawlerSpecificCommandDetailPresentationField
	addTextField := func(label, val string) {
		if strings.TrimSpace(val) != "" {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
				FieldDisplayName: label,
				FieldValue:       textValue(val),
			})
		}
	}
	var paidDate *presentation.CalendarDate
	if clock := m.GetPaymentTime(); clock != nil {
		paidDate = clock.GetPaymentLocalDate()
		value := fmt.Sprintf("%s %02d:%02d", calendarDateText(paidDate), clock.GetPaymentLocalHour(), clock.GetPaymentLocalMinute())
		if method := moneyMethodText(m); method != "" {
			value += ", by " + method
		}
		addTextField("Paid", value)
	} else if m.GetChargeDate() != nil {
		paidDate = m.GetChargeDate()
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Charged",
			FieldValue:       calendarDateValue(paidDate),
		})
	}
	if m.GetBookingDate() != nil && !proto.Equal(m.GetBookingDate(), paidDate) {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Booked",
			FieldValue:       calendarDateValue(m.GetBookingDate()),
		})
	}
	if m.GetValueDate() != nil && !proto.Equal(m.GetValueDate(), paidDate) && !proto.Equal(m.GetValueDate(), m.GetBookingDate()) {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Value date",
			FieldValue:       calendarDateValue(m.GetValueDate()),
		})
	}
	if m.GetOriginalForeignCurrencyAmount() != nil {
		value := render.FormatExactMonetaryAmount(presentationAmount(m.GetOriginalForeignCurrencyAmount(), false))
		if rate := m.GetCurrencyExchangeRate(); rate != nil {
			value += " at " + rate.GetQuoteCurrencyUnitsPerBaseCurrencyUnit() + " " + rate.GetQuoteCurrencyCode() + " per " + rate.GetBaseCurrencyCode()
		}
		if m.GetForeignCurrencyCommission() != nil {
			value += "; charge includes " + render.FormatExactMonetaryAmount(presentationAmount(m.GetForeignCurrencyCommission(), false)) + " commission"
		}
		addTextField("Original", value)
	}
	if m.GetOriginalForeignCurrencyAmount() == nil && m.GetCurrencyExchangeRate() != nil {
		rate := m.GetCurrencyExchangeRate()
		addTextField("Exchange rate", rate.GetQuoteCurrencyUnitsPerBaseCurrencyUnit()+" "+rate.GetQuoteCurrencyCode()+" per "+rate.GetBaseCurrencyCode())
	}
	if h := m.GetHotelStay(); h != nil {
		addTextField("Stay", calendarDateText(h.GetArrivalDate())+" – "+calendarDateText(h.GetDepartureDate()))
	}
	if f := m.GetFlightTicket(); f != nil {
		addTextField("Flight", f.GetDeparturePlace()+" → "+f.GetArrivalPlace()+", "+f.GetAirlineCode()+", class "+f.GetBookingClassCode())
		addTextField("Passenger", f.GetPassengerName())
		addTextField("Ticket", f.GetTicketNumber())
	}
	if m.GetCardStatementEndDate() != nil {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Statement closes",
			FieldValue:       calendarDateValue(m.GetCardStatementEndDate()),
		})
	}
	if m.GetCounterpartyAccountIdentifier() != "" {
		label := "To"
		if !strings.HasPrefix(m.GetSignedAmountIntoMoneyAccount().GetExactDecimalAmount(), "-") {
			label = "From"
		}
		addTextField(label, groupMoneyAccountIdentifier(m.GetCounterpartyAccountIdentifier()))
	}
	addTextField("Reference", m.GetPaymentReference())
	if account.GetCanonicalRecordReference() != nil {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Account",
			FieldValue:       refValue(account.GetCanonicalRecordReference()),
		})
	}

	var bodyParts []string
	if strings.TrimSpace(m.GetMovementDescription()) != "" {
		bodyParts = append(bodyParts, strings.TrimSpace(m.GetMovementDescription()))
	}
	for _, detail := range m.GetAdditionalPaymentDetails() {
		if strings.TrimSpace(detail) != "" {
			bodyParts = append(bodyParts, strings.TrimSpace(detail))
		}
	}
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       title,
		DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier(openrecord.MoneyDetailAnchorID),
		DetailSubtitle:          subtitle,
		FieldsInDisplayOrder:    fields,
	}
	if len(bodyParts) > 0 {
		detail.Body = &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: strings.Join(bodyParts, "\n")}
	}
	if providerDisplayName != "" || (providerReadTime != nil && providerReadTime.IsValid()) {
		detail.DetailFooterText = "From " + providerDisplayName + ", read " + formatOpenedTimestamp(providerReadTime) + "."
	}
	if m.GetCounterpartyDisplayName() != "" {
		detail.NextCommandsInDisplayOrder = append(detail.NextCommandsInDisplayOrder, &presentation.DeclarativeTrawlCommand{
			CommandLabel: "Search",
			CommandArgumentsAfterTrawlInvocation: []*presentation.DeclarativeTrawlCommandArgument{
				declarativeTextArg("search"),
				declarativeTextArg(m.GetCounterpartyDisplayName()),
			},
		})
	}
	if account.GetCanonicalRecordReference() != nil {
		detail.NextCommandsInDisplayOrder = append(detail.NextCommandsInDisplayOrder, &presentation.DeclarativeTrawlCommand{
			CommandLabel: "More",
			CommandArgumentsAfterTrawlInvocation: []*presentation.DeclarativeTrawlCommandArgument{
				declarativeTextArg("money"),
				declarativeTextArg("transactions"),
				declarativeTextArg("--account"),
				declarativeRefArg(account.GetCanonicalRecordReference()),
			},
		})
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{DetailPresentation: detail}
}

func buildOpenedMoneyPendingMovementPresentation(
	m *contract.MoneyPendingMovement,
	account *contract.MoneyAccount,
	providerDisplayName string,
	providerReadTime *timestamppb.Timestamp,
) *open.TrawlerSpecificOpenedRecordPresentation {
	fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{
		{FieldDisplayName: "Status", FieldValue: textValue("Pending")},
		{FieldDisplayName: "Amount", FieldValue: amountValue(m.GetSignedAmountIntoMoneyAccount(), true)},
		{FieldDisplayName: "Account", FieldValue: textValue(account.GetMoneyAccountDisplayName())},
		{FieldDisplayName: "Charged", FieldValue: calendarDateValue(m.GetChargeDate())},
	}
	if account.GetCanonicalRecordReference() != nil {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Account link",
			FieldValue:       refValue(account.GetCanonicalRecordReference()),
		})
	}
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       m.GetCounterpartyDisplayName(),
		DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier(openrecord.MoneyDetailAnchorID),
		FieldsInDisplayOrder:    fields,
	}
	if strings.TrimSpace(m.GetMovementDescription()) != "" {
		detail.Body = &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: strings.TrimSpace(m.GetMovementDescription())}
	}
	if providerDisplayName != "" || (providerReadTime != nil && providerReadTime.IsValid()) {
		detail.DetailFooterText = "Read from " + providerDisplayName + ": " + formatOpenedTimestamp(providerReadTime)
	}
	if account.GetCanonicalRecordReference() != nil {
		detail.NextCommandsInDisplayOrder = []*presentation.DeclarativeTrawlCommand{{
			CommandLabel: "More",
			CommandArgumentsAfterTrawlInvocation: []*presentation.DeclarativeTrawlCommandArgument{
				declarativeTextArg("money"),
				declarativeTextArg("transactions"),
				declarativeTextArg("--account"),
				declarativeRefArg(account.GetCanonicalRecordReference()),
				declarativeTextArg("--pending"),
			},
		}}
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{DetailPresentation: detail}
}
