package money

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	contract "github.com/opentrawl/opentrawl/trawlers/money/proto/trawl/money"
	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
	"google.golang.org/protobuf/proto"
)

const moneyIndexBatchSize = 2000

// ExportArchiveIndexChanges streams financial accounts (for short references)
// and booked/pending money movements (with structured FinancialMovementIndexFacts
// and exact minor-unit amount anchors) into core index.db.
func (*App) ExportArchiveIndexChanges(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	if req.OpenedTrawlerArchiveStore == nil {
		return nil
	}
	db := req.OpenedTrawlerArchiveStore.DB()

	var acctCount, movCount, pendingCount int64
	var lastRead string
	if err := db.QueryRowContext(ctx, `select count(*) from money_accounts`).Scan(&acctCount); err != nil {
		return fmt.Errorf("count money accounts for index cursor: %w", err)
	}
	if err := db.QueryRowContext(
		ctx,
		`select count(*), coalesce(sum(pending), 0), coalesce(max(read_time), '') from money_movements`,
	).Scan(&movCount, &pendingCount, &lastRead); err != nil {
		return fmt.Errorf("read money index cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("money:%d:%d:%d:%s", acctCount, movCount, pendingCount, lastRead)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	accountShortRefs, err := loadMoneyAccountShortRefs(ctx, db)
	if err != nil {
		return err
	}

	rows, err := db.QueryContext(ctx, `select reference, pending, record, provider from money_movements order by rowid`)
	if err != nil {
		return fmt.Errorf("read money movements for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, min(int(movCount), moneyIndexBatchSize))
	shortRefOnly := accountShortRefs
	firstBatch := true
	flush := func(isFinal bool) error {
		batch := &indexcontract.ArchiveIndexChangeBatch{
			Upserts:                     upserts,
			ShortReferenceOnlyEntries:   shortRefOnly,
			CompleteSnapshotReplacement: firstBatch,
		}
		firstBatch = false
		if isFinal {
			batch.NextOpaqueCursor = nextCursor
		}
		if err := emit(batch); err != nil {
			return err
		}
		if isFinal {
			upserts = nil
		} else {
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, moneyIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	for rows.Next() {
		var reference, provider string
		var pending int
		var blob []byte
		if err := rows.Scan(&reference, &pending, &blob, &provider); err != nil {
			return fmt.Errorf("scan money movement for unified index: %w", err)
		}
		upsert := buildMoneyMovementUpsert(reference, provider, pending != 0, blob)
		if upsert == nil {
			canonical := trawlkit.NewCanonicalArchiveRecordReference(reference)
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		}
		upserts = append(upserts, upsert)
		if len(upserts)+len(shortRefOnly) >= moneyIndexBatchSize {
			if err := flush(false); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush(true)
}

func loadMoneyAccountShortRefs(ctx context.Context, db *sql.DB) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := db.QueryContext(ctx, `select reference from money_accounts order by reference`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var reference string
		if err := rows.Scan(&reference); err != nil {
			return nil, err
		}
		if strings.TrimSpace(reference) == "" {
			continue
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(reference)
		out = append(out, &indexcontract.ShortReferenceOnlyRegistrationEntry{
			StableRecordReference:   canonical,
			ResolvedRecordReference: canonical,
		})
	}
	return out, rows.Err()
}

func buildMoneyMovementUpsert(reference, provider string, isPending bool, blob []byte) *indexcontract.IndexedArchiveRecordUpsert {
	var (
		decimalAmount, currencyCode         string
		origDecimalAmount, origCurrencyCode string
		bookingDate                         string
		occUnix                             int64
		hasTime                             bool
		counterparty, description, payRef   string
		merchantCity, merchantCountry       string
	)
	if isPending {
		var mv contract.MoneyPendingMovement
		if err := proto.Unmarshal(blob, &mv); err != nil {
			return nil
		}
		if amt := mv.GetSignedAmountIntoMoneyAccount(); amt != nil {
			decimalAmount = amt.GetExactDecimalAmount()
			currencyCode = amt.GetIso_4217CurrencyCode()
		}
		bookingDate, occUnix, hasTime = moneyCalendarDateToUnix(mv.GetChargeDate())
		counterparty = strings.TrimSpace(mv.GetCounterpartyDisplayName())
		description = strings.TrimSpace(mv.GetMovementDescription())
	} else {
		var mv contract.MoneyMovement
		if err := proto.Unmarshal(blob, &mv); err != nil {
			return nil
		}
		if amt := mv.GetSignedAmountIntoMoneyAccount(); amt != nil {
			decimalAmount = amt.GetExactDecimalAmount()
			currencyCode = amt.GetIso_4217CurrencyCode()
		}
		if orig := mv.GetOriginalForeignCurrencyAmount(); orig != nil {
			origDecimalAmount = orig.GetExactDecimalAmount()
			origCurrencyCode = orig.GetIso_4217CurrencyCode()
		}
		date := mv.GetBookingDate()
		if date == nil {
			date = mv.GetChargeDate()
		}
		bookingDate, occUnix, hasTime = moneyCalendarDateToUnix(date)
		counterparty = strings.TrimSpace(mv.GetCounterpartyDisplayName())
		description = strings.TrimSpace(mv.GetMovementDescription())
		payRef = strings.TrimSpace(mv.GetPaymentReference())
		merchantCity = strings.TrimSpace(mv.GetMerchantCity())
		merchantCountry = strings.TrimSpace(mv.GetMerchantCountryName())
	}

	canonical := trawlkit.NewCanonicalArchiveRecordReference(reference)
	title := strings.TrimSpace(strings.Join([]string{counterparty, merchantCity}, " "))
	if title == "" {
		title = description
	}
	body := strings.TrimSpace(strings.Join([]string{description, payRef}, " "))
	meta := strings.TrimSpace(strings.Join([]string{provider, merchantCity, merchantCountry, origDecimalAmount, origCurrencyCode}, " "))
	convKey := reference
	if counterparty != "" {
		convKey = "money:counterparty/" + strings.ToLower(counterparty)
	}

	fin := &indexcontract.FinancialMovementIndexFacts{
		ExactDecimalAmount:                  decimalAmount,
		Iso_4217CurrencyCode:                currencyCode,
		OriginalForeignExactDecimalAmount:   origDecimalAmount,
		OriginalForeignIso_4217CurrencyCode: origCurrencyCode,
		BookingCalendarDate:                 bookingDate,
		CounterpartyDisplayName:             counterparty,
		MovementDescription:                 description,
		MerchantCityName:                    merchantCity,
		MerchantCountryName:                 merchantCountry,
		MovementIsPending:                   isPending,
	}
	var anchors []*indexcontract.MechanicalJoinAnchor
	if signedMinor, ok := unifiedindex.ParseDecimalStringToMinorUnits(decimalAmount); ok {
		absMinor := signedMinor
		if absMinor < 0 {
			absMinor = -absMinor
		}
		fin.SignedMinorUnits = signedMinor
		fin.HasSignedMinorUnits = true
		fin.AbsoluteMinorUnits = absMinor
		fin.HasAbsoluteMinorUnits = true
		if absMinor > 0 {
			anchors = append(anchors, &indexcontract.MechanicalJoinAnchor{
				AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_EXACT_MINOR_UNIT_AMOUNT,
				AnchorValue: strconv.FormatInt(absMinor, 10),
			})
		}
	}
	if origMinor, ok := unifiedindex.ParseDecimalStringToMinorUnits(origDecimalAmount); ok {
		absOrig := origMinor
		if absOrig < 0 {
			absOrig = -absOrig
		}
		fin.OriginalForeignAbsoluteMinorUnits = absOrig
		fin.HasOriginalForeignAbsoluteMinorUnits = true
		if absOrig > 0 {
			anchors = append(anchors, &indexcontract.MechanicalJoinAnchor{
				AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_EXACT_MINOR_UNIT_AMOUNT,
				AnchorValue: strconv.FormatInt(absOrig, 10),
			})
		}
	}

	var participants []*indexcontract.ArchiveRecordParticipant
	if counterparty != "" {
		participants = []*indexcontract.ArchiveRecordParticipant{{
			DisplayName:     counterparty,
			ParticipantRole: "counterparty",
		}}
	}
	var statusLabels []string
	if isPending {
		statusLabels = []string{"Pending"}
	}

	return &indexcontract.IndexedArchiveRecordUpsert{
		StableRecordReference:          canonical,
		ResolvedRecordReference:        canonical,
		RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_FINANCIAL_MOVEMENT,
		ConversationOrThreadIdentifier: convKey,
		DisplayContextTitle:            strings.TrimSpace(provider),
		OccurredUnixSeconds:            occUnix,
		HasOccurredTime:                hasTime,
		HasTimeOfDay:                   false,
		Title:                          title,
		BodyText:                       body,
		MetadataText:                   meta,
		Participants:                   participants,
		StatusLabels:                   statusLabels,
		MechanicalJoinAnchors:          anchors,
		FinancialMovementFacts:         fin,
	}
}

func moneyCalendarDateToUnix(date interface {
	GetCalendarYear() int32
	GetCalendarMonthNumber() int32
	GetCalendarDayOfMonth() int32
}) (string, int64, bool) {
	if date == nil || date.GetCalendarYear() == 0 || date.GetCalendarMonthNumber() == 0 || date.GetCalendarDayOfMonth() == 0 {
		return "", 0, false
	}
	t := time.Date(int(date.GetCalendarYear()), time.Month(date.GetCalendarMonthNumber()), int(date.GetCalendarDayOfMonth()), 0, 0, 0, 0, time.UTC)
	return t.Format("2006-01-02"), t.Unix(), true
}
