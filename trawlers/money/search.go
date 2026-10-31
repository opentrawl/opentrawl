package money

import (
	"context"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	contract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type moneyMatchingField struct {
	snippet string
	label   string
}

func matchingMoneyMovementFields(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, text string) (map[string]moneyMatchingField, error) {
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, "SELECT reference,field_label,"+store.FTS5MarkedSearchResultSnippetSQLExpression("money_search", 2)+" FROM money_search WHERE money_search MATCH ? ORDER BY field_label='Transaction' DESC", store.FTS5TokenQuery(text))
	if err != nil {
		return nil, err
	}
	snippets := map[string]moneyMatchingField{}
	for rows.Next() {
		var reference, label, snippet string
		if err := rows.Scan(&reference, &label, &snippet); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if _, exists := snippets[reference]; !exists {
			snippets[reference] = moneyMatchingField{snippet, label}
		}
	}
	err = rows.Err()
	_ = rows.Close()
	return snippets, err
}

func (*App) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	response := &search.TrawlerSearchResponse{}
	if strings.TrimSpace(query.Text) == "" || query.PersonFilter.ResolvedPersonFilter() != nil || query.PersonFilter.UnresolvedPersonFilterText() != "" {
		return response, nil
	}
	snippets, err := matchingMoneyMovementFields(ctx, req, query.Text)
	if err != nil {
		return nil, err
	}
	for _, pending := range []bool{false, true} {
		movements, err := readMovements(ctx, req, pending)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(movements, func(i, j int) bool { return movements[i].date("booked") > movements[j].date("booked") })
		for _, movement := range movements {
			snippet, matched := snippets[movement.reference()]
			if !matched {
				continue
			}
			date := movement.date("booked")
			if !query.After.IsZero() && date < query.After.Format("2006-01-02") {
				continue
			}
			if !query.Before.IsZero() && date > query.Before.Format("2006-01-02") {
				continue
			}
			var amount *contract.ExactMonetaryAmount
			var calendarDate *presentation.CalendarDate
			if pending {
				amount = movement.pending.SignedAmountIntoMoneyAccount
				calendarDate = movement.pending.ChargeDate
			} else {
				amount = movement.booked.SignedAmountIntoMoneyAccount
				calendarDate = movement.booked.BookingDate
				if calendarDate == nil {
					calendarDate = movement.booked.ChargeDate
				}
			}
			response.TotalSearchMatches++

			response.TrawlerSearchMatchesInDisplayOrder = append(response.TrawlerSearchMatchesInDisplayOrder, &search.TrawlerSearchMatch{RecordAnchor: trawlkit.NewRecordAnchorIdentifier(openrecord.MoneyDetailAnchorID), CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(movement.reference()), SearchMatchPresentation: &search.SearchMatchPresentation{MatchingRecordKindDisplayName: "transaction", MatchingRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay{ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{CalendarDate: calendarDate}}, MoneyMovementSearchMatch: &contract.MoneyMovementSearchMatch{SignedAmountIntoMoneyAccount: amount, MoneyMovementIsPending: pending}, SearchMatchTextFieldsInDisplayOrder: []*search.SearchMatchTextField{trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(snippet.label, store.ParseFTS5MarkedText(snippet.snippet))}}})
		}
	}
	sort.SliceStable(response.TrawlerSearchMatchesInDisplayOrder, func(i, j int) bool {
		return calendarDateText(response.TrawlerSearchMatchesInDisplayOrder[i].SearchMatchPresentation.MatchingRecordAssociatedTime.GetCalendarDate()) > calendarDateText(response.TrawlerSearchMatchesInDisplayOrder[j].SearchMatchPresentation.MatchingRecordAssociatedTime.GetCalendarDate())
	})
	if query.Limit > 0 && len(response.TrawlerSearchMatchesInDisplayOrder) > query.Limit {
		response.TrawlerSearchMatchesInDisplayOrder = response.TrawlerSearchMatchesInDisplayOrder[:query.Limit]
		response.MoreSearchMatchesExist = true
	}
	return response, nil
}
