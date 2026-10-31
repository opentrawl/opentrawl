// Money account and movement presentation.
package render

import (
	"fmt"
	"strings"

	money "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func moneyAccountName(a *money.MoneyAccount) string { return a.GetMoneyAccountDisplayName() }
func moneyReadTime(t *timestamppb.Timestamp) string {
	return formatExactTimeInThisComputerTimeZone(t, localTimeWithUTCOffsetForListRow)
}
func moneyBalanceBasis(b *money.MoneyBalance) string {

	return map[money.MoneyBalanceBasis]string{
		money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_CURRENT:             "owed now",
		money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_BOOKED:                   "booked",
		money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_AVAILABLE:                "available",
		money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_EXPECTED:                 "including pending",
		money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_STATEMENT:           "statement",
		money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_PREVIOUS_CARD_STATEMENT:  "previous statement",
		money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_REMAINING_CARD_STATEMENT: "remaining statement",
	}[b.GetMoneyBalanceBasis()]
}

func moneyBalanceAmountForDisplay(balance *money.MoneyBalance) string {
	amount := balance.GetAccountBalanceAmount()
	if balance.GetMoneyBalanceBasis() == money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_CURRENT && amount != nil {
		amount = proto.Clone(amount).(*money.ExactMonetaryAmount)
		if strings.HasPrefix(amount.ExactDecimalAmount, "-") {
			amount.ExactDecimalAmount = strings.TrimPrefix(amount.ExactDecimalAmount, "-")
		} else if strings.Trim(amount.ExactDecimalAmount, "0.") != "" {
			amount.ExactDecimalAmount = "-" + amount.ExactDecimalAmount
		}
	}
	return FormatMoneyAmount(amount, false)
}

func moneyBalanceObservation(balance *money.MoneyBalance, writtenForm archiveEventTimeWrittenForm) (string, string) {
	if asOf := formatArchiveRecordAssociatedTime(balance.GetBalanceAsOf(), writtenForm); asOf != "" {
		return "As of", asOf
	}
	if read := formatExactTimeInThisComputerTimeZone(balance.GetBalanceReadTime(), writtenForm); read != "" {
		return "Read", read
	}
	return "Read", "not recorded"
}
func (o TrawlCommandOutput) WriteMoneyAccounts(r *money.MoneyAccountListResponse, links map[string]string) error {
	var balances, history [][]string
	for _, a := range r.GetMoneyAccountsInDisplayOrder() {
		link := links[a.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference()]
		balancesToShow := a.GetMoneyAccountBalances()
		if a.GetMoneyCardBilling() != nil {
			for _, b := range balancesToShow {
				if b.GetMoneyBalanceBasis() == money.MoneyBalanceBasis_MONEY_BALANCE_BASIS_CARD_CURRENT {
					balancesToShow = []*money.MoneyBalance{b}
					break
				}
			}
		}
		for _, b := range balancesToShow {
			label, observed := moneyBalanceObservation(b, localTimeWithUTCOffsetForListRow)
			if label == "Read" {
				observed = "read " + observed
			}
			balances = append(balances, []string{moneyAccountName(a), moneyBalanceAmountForDisplay(b), moneyBalanceBasis(b), observed, link})
		}
		if len(balancesToShow) == 0 {
			balances = append(balances, []string{moneyAccountName(a), "unknown", "not read", "", link})
		}
		for _, interval := range a.GetCompleteHistoryDateRanges() {
			dateBasis := "booked"
			if interval.GetMovementDateBasis() == money.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_CHARGE_DATE {
				dateBasis = "charged"
			}
			history = append(history, []string{moneyAccountName(a), formatCalendarDate(interval.GetFirstCompleteDate()) + " – " + formatCalendarDate(interval.GetLastCompleteDate()), dateBasis, moneyReadTime(a.GetLastBookedMovementsReadTime())})
		}
		if len(a.GetCompleteHistoryDateRanges()) == 0 {
			history = append(history, []string{moneyAccountName(a), "not established", "", moneyReadTime(a.GetLastBookedMovementsReadTime())})
		}
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(o.standardOutput, []TableColumn{{Header: "account", Wrap: true, MinimumWidth: 15, RequiredMinimumWidth: 11}, {Header: "balance", AlignRight: true, NeverTruncateCellValues: true}, {Header: "basis", Wrap: true, RequiredMinimumWidth: 9}, {Header: "as of / read", NeverTruncateCellValues: true}, {Header: "link", NeverTruncateCellValues: true}}, balances, 2); err != nil {
		return err
	}
	if err := writeLine(o.standardOutput, ""); err != nil {
		return err
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(o.standardOutput, []TableColumn{{Header: "account", NeverTruncateCellValues: true}, {Header: "complete history", RequiredMinimumWidth: 23}, {Header: "basis", NeverTruncateCellValues: true}, {Header: "last read", NeverTruncateCellValues: true}}, history, 1); err != nil {
		return err
	}
	for _, a := range r.GetMoneyAccountsInDisplayOrder() {
		if a.GetHistoryAvailabilityNote() != "" {
			if err := writeLine(o.standardOutput, ""); err != nil {
				return err
			}
			if err := writeLines(o.standardOutput, Wrap(moneyAccountName(a)+": "+a.GetHistoryAvailabilityNote(), OutputWidth(o.standardOutput))); err != nil {
				return err
			}
		}
	}
	return nil
}
func (o TrawlCommandOutput) WriteMoneyTransactions(r *money.MoneyMovementListResponse, links map[string]string, moreArguments []string) error {
	count := 0
	for _, g := range r.GetMoneyAccountMovementLists() {
		count += len(g.GetBookedMovementsNewestFirst()) + len(g.GetPendingMovementsNewestFirst())
	}
	kind := "Transactions"
	if r.GetListsPendingMovements() {
		kind = "Pending"
	}
	if err := writeLine(o.standardOutput, fmt.Sprintf("%s: showing %d of %s.", kind, count, FormatInteger(int64(r.GetTotalMatchingMovementCount())))); err != nil {
		return err
	}
	var firstLink string
	hasChargeDateFallback := false
	for _, g := range r.GetMoneyAccountMovementLists() {
		if err := writeLine(o.standardOutput, "\n"+moneyAccountName(g.GetMoneyAccount())); err != nil {
			return err
		}
		var rows [][]string
		for _, m := range g.GetBookedMovementsNewestFirst() {
			date := formatCalendarDate(m.GetBookingDate())
			if r.GetMovementDateBasis() == money.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_CHARGE_DATE {
				date = formatCalendarDate(m.GetChargeDate())
			}
			if date == "" {
				date = formatCalendarDate(m.GetChargeDate()) + "*"
				hasChargeDateFallback = true
			}
			rows = append(rows, []string{date, FormatMoneyAmount(m.GetSignedAmountIntoMoneyAccount(), true), m.GetCounterpartyDisplayName(), m.GetMovementDescription(), links[m.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference()]})
		}
		for _, m := range g.GetPendingMovementsNewestFirst() {
			rows = append(rows, []string{formatCalendarDate(m.GetChargeDate()), FormatMoneyAmount(m.GetSignedAmountIntoMoneyAccount(), true), m.GetCounterpartyDisplayName(), m.GetMovementDescription(), links[m.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference()]})
		}
		if len(rows) > 0 && firstLink == "" {
			firstLink = rows[0][4]
		}
		heading := "booked"
		if r.GetListsPendingMovements() || r.GetMovementDateBasis() == money.MoneyMovementDateBasis_MONEY_MOVEMENT_DATE_BASIS_CHARGE_DATE {
			heading = "charged"
		}
		columns := []TableColumn{{Header: heading, NeverTruncateCellValues: true}, {Header: "amount", AlignRight: true, NeverTruncateCellValues: true}, {Header: "counterparty", RequiredMinimumWidth: 24}, {Header: "description", MinimumWidth: 12}, {Header: "link", NeverTruncateCellValues: true}}
		primary := 3
		hasDescription := false
		for _, row := range rows {
			hasDescription = hasDescription || row[3] != ""
		}
		if !hasDescription {
			columns = append(columns[:3], columns[4:]...)
			for i, row := range rows {
				rows[i] = append(row[:3], row[4:]...)
			}
			primary = 2
		}
		if err := writeHumanRecordRowsWithPrimaryContentColumn(o.standardOutput, columns, rows, primary); err != nil {
			return err
		}
	}
	if hasChargeDateFallback {
		if err := writeLine(o.standardOutput, "\n* Charge date; booking date not supplied."); err != nil {
			return err
		}
	}
	var next []NextCommand
	if firstLink != "" {
		next = append(next, nextCommandOpeningRecord(firstLink))
	}
	if r.GetMoreMatchingMovementsExist() {
		next = append(next, NextCommandShowingMoreRows(moreArguments))
	}
	return writeNextCommands(o.standardOutput, next)
}
func (o TrawlCommandOutput) WriteMoneyBookedOpen(r *money.OpenedMoneyMovement, links map[string]string) error {
	return o.WriteMoneyFullDetail(r, links)
}
func (o TrawlCommandOutput) WriteMoneyPendingOpen(r *money.OpenedMoneyPendingMovement, links map[string]string) error {
	m := r.GetMoneyPendingMovement()
	a := r.GetMoneyAccount()
	if err := writeCard(o.standardOutput, card{title: m.GetCounterpartyDisplayName(), fields: []cardField{{"Status", "Pending"}, {"Amount", FormatMoneyAmount(m.GetSignedAmountIntoMoneyAccount(), true)}, {"Account", moneyAccountName(a)}, {"Charged", formatCalendarDate(m.GetChargeDate())}}, body: m.GetMovementDescription()}); err != nil {
		return err
	}
	if err := writeLine(o.standardOutput, ""); err != nil {
		return err
	}
	if err := writeWrappedField(o.standardOutput, "Read from "+r.GetProviderDisplayName(), formatExactTimeInThisComputerTimeZone(r.GetProviderReadTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord)); err != nil {
		return err
	}
	return writeNextCommands(o.standardOutput, []NextCommand{nextCommandWithLabel(nextCommandLabelMoreRows, "money", "transactions", "--account", links[a.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference()], "--pending")})
}

func moneyRenderLinks(links GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference) map[string]string {
	mapped := make(map[string]string, len(links))
	for _, link := range links {
		mapped[canonicalArchiveRecordReferenceText(link.CanonicalArchiveRecordReference)] = globallyRoutableTrawlLinkText(link.GloballyRoutableTrawlLink)
	}
	return mapped
}
func (output TrawlCommandOutput) WriteMoneyAccountDetail(account *money.MoneyAccount, links map[string]string) error {
	if err := writeLine(output.standardOutput, account.GetMoneyAccountDisplayName()); err != nil {
		return err
	}
	for _, balance := range account.GetMoneyAccountBalances() {
		if err := writeWrappedField(output.standardOutput, moneyBalanceBasis(balance), moneyBalanceAmountForDisplay(balance)); err != nil {
			return err
		}
		label, observed := moneyBalanceObservation(balance, localTimeWithUTCOffsetAndUTCForOpenedRecord)
		if err := writeWrappedField(output.standardOutput, label, observed); err != nil {
			return err
		}
	}
	if len(account.GetMoneyAccountBalances()) == 0 {
		if err := writeWrappedField(output.standardOutput, "Balance", "unknown; not read"); err != nil {
			return err
		}
	}
	if note := account.GetHistoryAvailabilityNote(); note != "" {
		if err := writeWrappedField(output.standardOutput, "History", note); err != nil {
			return err
		}
	}
	return writeNextCommands(output.standardOutput, []NextCommand{nextCommandWithLabel(nextCommandLabelMoreRows, "money", "transactions", "--account", links[canonicalArchiveRecordReferenceText(account.GetCanonicalRecordReference())])})
}
