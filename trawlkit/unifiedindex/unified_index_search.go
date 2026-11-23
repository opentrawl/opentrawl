package unifiedindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/flags"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	personcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	searchcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UnifiedSearchQuery configures a ranked search over core index.db.
type UnifiedSearchQuery struct {
	QueryText         string
	Trawlers          []string
	AfterUnix         *int64
	BeforeUnix        *int64
	PersonFilterTerms []string
	Limit             int
	CollapseThreads   bool
}

// UnifiedSearchOutput holds ranked FederatedSearchMatch records ready for both
// the CLI renderer and the Mac app protobuf bridge.
type UnifiedSearchOutput struct {
	Matches               []*federationcontract.FederatedSearchMatch
	TotalRawMatches       uint64
	TotalMatchesByTrawler map[string]uint64
	MoreMatchesExist      bool
	IndexedTrawlerNames   []string
	Elapsed               time.Duration
}

type indexedRecordCandidate struct {
	docID             int64
	trawler           string
	trawlerDisplay    string
	recordKind        string
	sourceRole        string
	canonicalRef      string
	resolvedRef       string
	recordAnchor      string
	conversationKey   string
	displayContext    string
	occurredUnix      sql.NullInt64
	hasTimeOfDay      int
	utcOffsetSeconds  int
	title             string
	peopleText        string
	bodyText          string
	metaText          string
	observedLabels    string
	statusLabels      string
	moneyDecimal      string
	moneyCurrency     string
	moneyPending      int
	rawBM25           float64
	finalScore        float64
	collapsedSiblings int
}

// SearchUnifiedIndex executes global BM25 + recency + observed-role/label
// ranking with conversation collapse and cross-trawler diversity capping.
func SearchUnifiedIndex(ctx context.Context, db *sql.DB, q UnifiedSearchQuery) (UnifiedSearchOutput, error) {
	started := time.Now()
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	nowUnix := time.Now().Unix()

	candidates, totalRaw, totalsByTrawler, err := fetchRankedCandidates(ctx, db, q, limit, nowUnix)
	if err != nil {
		return UnifiedSearchOutput{}, err
	}
	if len(candidates) == 0 {
		return UnifiedSearchOutput{
			TotalRawMatches:       totalRaw,
			TotalMatchesByTrawler: totalsByTrawler,
			Elapsed:               time.Since(started),
		}, nil
	}

	if q.CollapseThreads {
		candidates = collapseCandidatesByConversation(candidates)
	}
	multiTrawler := len(q.Trawlers) != 1
	selected := applyPerTrawlerDiversityCap(candidates, limit, multiTrawler)
	moreExist := len(candidates) > len(selected) || totalRaw > uint64(len(selected))

	links, err := resolveShortLinksForCandidates(ctx, db, selected)
	if err != nil {
		return UnifiedSearchOutput{}, err
	}

	queryTokens := TokenizeSearchWords(q.QueryText)
	matches := make([]*federationcontract.FederatedSearchMatch, 0, len(selected))
	for _, cand := range selected {
		link := links[cand.trawler+"\x00"+cand.resolvedRef]
		if link == "" {
			link = cand.trawler + ":" + shortref.Alias(cand.canonicalRef, shortref.MinLength)
		}
		matches = append(matches, candidateToFederatedSearchMatch(cand, link, queryTokens))
	}

	return UnifiedSearchOutput{
		Matches:               matches,
		TotalRawMatches:       totalRaw,
		TotalMatchesByTrawler: totalsByTrawler,
		MoreMatchesExist:      moreExist,
		Elapsed:               time.Since(started),
	}, nil
}

func fetchRankedCandidates(
	ctx context.Context,
	db *sql.DB,
	q UnifiedSearchQuery,
	limit int,
	nowUnix int64,
) ([]indexedRecordCandidate, uint64, map[string]uint64, error) {
	ftsExpr := buildFTS5QueryExpression(q.QueryText)
	poolSize := max(limit*12, 240)
	if poolSize > 1200 {
		poolSize = 1200
	}

	var (
		whereClauses []string
		args         []any
	)
	if ftsExpr != "" {
		whereClauses = append(whereClauses, `indexed_archive_records_fts MATCH ?`)
		args = append(args, ftsExpr)
	}
	if len(q.Trawlers) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.Trawlers)), ",")
		whereClauses = append(whereClauses, `r.trawler IN (`+ph+`)`)
		for _, t := range q.Trawlers {
			args = append(args, strings.TrimSpace(t))
		}
	}
	flags.InclusiveTimeRangeFromUnixBounds(q.AfterUnix, q.BeforeUnix).
		AppendUnixSQLClauses("r.occurred_unix", &whereClauses, &args)
	if clause, pArgs := buildPersonFilterSQLClause(q.PersonFilterTerms); clause != "" {
		whereClauses = append(whereClauses, clause)
		args = append(args, pArgs...)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	var querySQL string
	if ftsExpr != "" {
		querySQL = `
			SELECT r.doc_id, r.trawler, r.trawler_display_name, r.record_kind, r.source_role,
			       r.canonical_ref, r.resolved_ref, r.record_anchor, r.conversation_key, r.display_context,
			       r.occurred_unix, r.has_time_of_day, r.utc_offset_seconds,
			       r.title, r.people_text, r.body_text, r.meta_text, r.observed_labels_text, r.status_labels_text,
			       r.money_decimal_amount, r.money_currency_code, r.money_is_pending,
			       bm25(indexed_archive_records_fts, 4.0, 2.0, 1.0, 1.5, 0.8) AS raw_bm25
			FROM indexed_archive_records_fts
			JOIN indexed_archive_records r ON r.doc_id = indexed_archive_records_fts.rowid` + whereSQL + `
			ORDER BY raw_bm25 ASC
			LIMIT ?`
	} else {
		querySQL = `
			SELECT r.doc_id, r.trawler, r.trawler_display_name, r.record_kind, r.source_role,
			       r.canonical_ref, r.resolved_ref, r.record_anchor, r.conversation_key, r.display_context,
			       r.occurred_unix, r.has_time_of_day, r.utc_offset_seconds,
			       r.title, r.people_text, r.body_text, r.meta_text, r.observed_labels_text, r.status_labels_text,
			       r.money_decimal_amount, r.money_currency_code, r.money_is_pending,
			       -1.0 AS raw_bm25
			FROM indexed_archive_records r` + whereSQL + `
			ORDER BY coalesce(r.occurred_unix, 0) DESC
			LIMIT ?`
	}

	queryArgs := append(append([]any(nil), args...), poolSize)
	rows, err := db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("query unified search index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	candidates := make([]indexedRecordCandidate, 0, min(poolSize, 256))
	for rows.Next() {
		var c indexedRecordCandidate
		if err := rows.Scan(
			&c.docID, &c.trawler, &c.trawlerDisplay, &c.recordKind, &c.sourceRole,
			&c.canonicalRef, &c.resolvedRef, &c.recordAnchor, &c.conversationKey, &c.displayContext,
			&c.occurredUnix, &c.hasTimeOfDay, &c.utcOffsetSeconds,
			&c.title, &c.peopleText, &c.bodyText, &c.metaText, &c.observedLabels, &c.statusLabels,
			&c.moneyDecimal, &c.moneyCurrency, &c.moneyPending,
			&c.rawBM25,
		); err != nil {
			return nil, 0, nil, fmt.Errorf("scan unified search candidate: %w", err)
		}
		c.finalScore = scoreCandidate(c, ftsExpr != "", nowUnix)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, err
	}
	_ = rows.Close()

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].finalScore != candidates[j].finalScore {
			return candidates[i].finalScore > candidates[j].finalScore
		}
		return candidates[i].occurredUnix.Int64 > candidates[j].occurredUnix.Int64
	})

	totalsByTrawler := make(map[string]uint64, 8)
	for _, c := range candidates {
		totalsByTrawler[c.trawler]++
	}
	totalRaw := uint64(len(candidates))
	if len(candidates) >= poolSize {
		countSQL := `SELECT r.trawler, count(*) FROM indexed_archive_records r` + whereSQL + ` GROUP BY r.trawler`
		if ftsExpr != "" {
			countSQL = `SELECT r.trawler, count(*) FROM indexed_archive_records_fts JOIN indexed_archive_records r ON r.doc_id = indexed_archive_records_fts.rowid` + whereSQL + ` GROUP BY r.trawler`
		}
		if countRows, countErr := db.QueryContext(ctx, countSQL, args...); countErr == nil {
			grouped := make(map[string]uint64, 8)
			var exact uint64
			for countRows.Next() {
				var trawlerID string
				var n uint64
				if scanErr := countRows.Scan(&trawlerID, &n); scanErr == nil && trawlerID != "" {
					grouped[trawlerID] = n
					exact += n
				}
			}
			_ = countRows.Close()
			if exact >= totalRaw && len(grouped) > 0 {
				totalRaw = exact
				totalsByTrawler = grouped
			}
		}
	}
	return candidates, totalRaw, totalsByTrawler, nil
}

func buildPersonFilterSQLClause(terms []string) (string, []any) {
	var cleaned []string
	for _, t := range terms {
		trimmed := strings.TrimSpace(strings.ToLower(t))
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(cleaned))
	args := make([]any, 0, len(cleaned)*2)
	for _, term := range cleaned {
		like := "%" + term + "%"
		parts = append(parts, `(lower(r.people_text) LIKE ? OR lower(r.display_context) LIKE ?)`)
		args = append(args, like, like)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func scoreCandidate(c indexedRecordCandidate, hasTextQuery bool, nowUnix int64) float64 {
	if !hasTextQuery {
		if c.occurredUnix.Valid {
			return float64(c.occurredUnix.Int64)
		}
		return 0
	}
	rel := -c.rawBM25
	if rel <= 0 {
		rel = 0.01
	}
	ageDays := 3650.0
	if c.occurredUnix.Valid && c.occurredUnix.Int64 > 0 {
		diff := float64(nowUnix-c.occurredUnix.Int64) / 86400.0
		if diff < 0 {
			diff = 0
		}
		ageDays = diff
	}
	recency := 1.0 + 0.35/(1.0+ageDays/180.0)
	roleMult := observedRoleMultiplier(c.sourceRole)
	labelMult := observedLabelMultiplier(c.observedLabels)
	return rel * recency * roleMult * labelMult
}

func observedRoleMultiplier(role string) float64 {
	switch role {
	case "authored":
		return 1.10
	case "bookmark":
		return 1.08
	case "mention":
		return 1.05
	case "like":
		return 0.45
	case "reply_context", "other":
		return 0.70
	default:
		return 1.00
	}
}

func observedLabelMultiplier(observedLabels string) float64 {
	if observedLabels == "" {
		return 1.0
	}
	// Strictly Gmail's own observed system category label, never heuristic guessing.
	if strings.Contains(observedLabels, "CATEGORY_PROMOTIONS") {
		return 0.55
	}
	return 1.0
}

func collapseCandidatesByConversation(candidates []indexedRecordCandidate) []indexedRecordCandidate {
	indexByKey := make(map[string]int, len(candidates))
	collapsed := make([]indexedRecordCandidate, 0, len(candidates))
	for _, c := range candidates {
		key := candidateCollapseKey(c)
		if key == "" {
			collapsed = append(collapsed, c)
			continue
		}
		if existingIdx, seen := indexByKey[key]; seen {
			collapsed[existingIdx].collapsedSiblings++
			continue
		}
		indexByKey[key] = len(collapsed)
		collapsed = append(collapsed, c)
	}
	return collapsed
}

func candidateCollapseKey(c indexedRecordCandidate) string {
	conv := strings.TrimSpace(c.conversationKey)
	if conv == "" {
		return ""
	}
	if c.recordKind == "message" && c.occurredUnix.Valid {
		dayBucket := c.occurredUnix.Int64 / 86400
		return fmt.Sprintf("%s:%s:%d", c.trawler, conv, dayBucket)
	}
	return c.trawler + ":" + conv
}

func applyPerTrawlerDiversityCap(candidates []indexedRecordCandidate, limit int, multiTrawler bool) []indexedRecordCandidate {
	if !multiTrawler || len(candidates) <= limit {
		if len(candidates) > limit {
			return candidates[:limit]
		}
		return candidates
	}
	perTrawlerCap := max(limit/2, 5)
	counts := make(map[string]int, 12)
	selected := make([]indexedRecordCandidate, 0, limit)
	var overflow []indexedRecordCandidate
	for _, c := range candidates {
		if counts[c.trawler] < perTrawlerCap {
			selected = append(selected, c)
			counts[c.trawler]++
			if len(selected) == limit {
				return selected
			}
			continue
		}
		overflow = append(overflow, c)
	}
	for _, c := range overflow {
		selected = append(selected, c)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func resolveShortLinksForCandidates(
	ctx context.Context,
	db *sql.DB,
	candidates []indexedRecordCandidate,
) (map[string]string, error) {
	refsByTrawler := make(map[string][]string, 8)
	for _, c := range candidates {
		refsByTrawler[c.trawler] = append(refsByTrawler[c.trawler], c.resolvedRef)
	}
	links := make(map[string]string, len(candidates))
	for trawler, refs := range refsByTrawler {
		aliases, err := shortref.NewScopedSQLiteIndex(db, trawler).Aliases(ctx, refs)
		if err != nil {
			return nil, err
		}
		for ref, alias := range aliases {
			links[trawler+"\x00"+ref] = trawler + ":" + alias
		}
	}
	return links, nil
}

func candidateToFederatedSearchMatch(
	c indexedRecordCandidate,
	link string,
	queryTokens []string,
) *federationcontract.FederatedSearchMatch {
	pres := &searchcontract.SearchMatchPresentation{
		RegisteredTrawlerDisplayName:  c.trawlerDisplay,
		MatchingRecordDisplayName:     displayNameForCandidate(c),
		MatchingRecordKindDisplayName: kindDisplayNameForCandidate(c),
		MatchingRecordAssociatedTime:  associatedTimeForCandidate(c),
	}
	if ctxTitle := strings.TrimSpace(c.displayContext); ctxTitle != "" && ctxTitle != pres.MatchingRecordDisplayName {
		pres.DigitalContainerNamesNearestToBroadest = []string{ctxTitle}
	}
	if ppl := CleanDisplayPeopleText(c.peopleText); ppl != "" {
		pres.PeopleRelatedToMatchingRecord = []*personcontract.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         TruncateRunes(ppl, 120),
			PersonRoleInArchiveRecord: personcontract.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_PARTICIPANT,
		}}
	}
	if c.moneyDecimal != "" && c.moneyCurrency != "" {
		pres.MatchingRecordMonetaryAmount = &presentation.ExactMonetaryAmount{
			ExactDecimalAmount:             c.moneyDecimal,
			Iso_4217CurrencyCode:           c.moneyCurrency,
			MonetaryAmountDisplaySignStyle: presentation.MonetaryAmountDisplaySignStyle_MONETARY_AMOUNT_DISPLAY_SIGN_STYLE_SIGNED_MOVEMENT,
		}
	}
	if c.statusLabels != "" {
		for _, lbl := range strings.Split(c.statusLabels, "\n") {
			if trimmed := strings.TrimSpace(lbl); trimmed != "" {
				pres.MatchingRecordStatusLabels = append(pres.MatchingRecordStatusLabels, trimmed)
			}
		}
	}
	if c.moneyPending != 0 && !containsString(pres.MatchingRecordStatusLabels, "Pending") {
		pres.MatchingRecordStatusLabels = append(pres.MatchingRecordStatusLabels, "Pending")
	}
	if c.collapsedSiblings > 0 {
		pres.MatchingRecordStatusLabels = append(
			pres.MatchingRecordStatusLabels,
			fmt.Sprintf("+%d in thread", c.collapsedSiblings),
		)
	}
	if body := strings.TrimSpace(c.bodyText); body != "" {
		fragments := BuildHighlightedMatchFragments(body, queryTokens, 220)
		if len(fragments) > 0 {
			pres.SearchMatchTextFieldsInDisplayOrder = []*searchcontract.SearchMatchTextField{{
				SearchMatchTextFieldName:               "",
				SearchMatchTextFragmentsInDisplayOrder: fragments,
			}}
		}
	}
	anchorText := strings.TrimSpace(c.recordAnchor)
	if anchorText == "" {
		anchorText = DefaultRecordAnchorForIndexedRecord(c.trawler, c.recordKind, strings.TrimSpace(c.bodyText) != "")
	}
	return &federationcontract.FederatedSearchMatch{
		RecordAnchor:            &identity.RecordAnchorIdentifier{RecordAnchorIdentifier: anchorText},
		SearchMatchPresentation: pres,
		TrawlLink:               &identity.GloballyRoutableTrawlLink{GloballyRoutableTrawlLink: link},
	}
}

// DefaultRecordAnchorForIndexedRecord returns the canonical non-empty record
// anchor expected by OpenCanonicalRecord when an indexed row did not store an
// explicit field-level anchor.
func DefaultRecordAnchorForIndexedRecord(trawlerID, recordKind string, hasBodyEvidence bool) string {
	switch strings.ToLower(strings.TrimSpace(trawlerID)) {
	case "gmail", "mail":
		if hasBodyEvidence {
			return "body"
		}
		return "subject"
	case "notes":
		return "body"
	case "calendar":
		return "summary"
	case "contacts":
		return "person_display_name"
	case "money":
		return "money-details"
	case "photos":
		return "photo-details"
	case "health":
		return "health-details"
	}
	switch strings.ToLower(strings.TrimSpace(recordKind)) {
	case "email":
		if hasBodyEvidence {
			return "body"
		}
		return "subject"
	case "note":
		return "body"
	case "event", "calendar":
		return "summary"
	case "person":
		return "person_display_name"
	case "photo":
		return "photo-details"
	default:
		return "match"
	}
}

func displayNameForCandidate(c indexedRecordCandidate) string {
	if title := strings.TrimSpace(c.title); title != "" {
		return TruncateRunes(title, 180)
	}
	if ctxTitle := strings.TrimSpace(c.displayContext); ctxTitle != "" {
		return TruncateRunes(ctxTitle, 180)
	}
	if ppl := strings.TrimSpace(c.peopleText); ppl != "" {
		return TruncateRunes(ppl, 180)
	}
	return TruncateRunes(strings.TrimSpace(c.bodyText), 120)
}

func kindDisplayNameForCandidate(c indexedRecordCandidate) string {
	switch c.recordKind {
	case "email":
		return "Email"
	case "message":
		return "Message"
	case "post":
		if c.sourceRole == "bookmark" {
			return "Bookmark"
		}
		if c.sourceRole == "like" {
			return "Like"
		}
		return "Post"
	case "note":
		return "Note"
	case "event":
		return "Event"
	case "photo":
		return "Photo"
	case "transaction":
		return "Movement"
	case "person":
		return "Person"
	case "health":
		return "Health"
	default:
		return ""
	}
}

func associatedTimeForCandidate(c indexedRecordCandidate) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	if !c.occurredUnix.Valid || c.occurredUnix.Int64 == 0 {
		return nil
	}
	t := time.Unix(c.occurredUnix.Int64, 0).UTC()
	if c.hasTimeOfDay == 0 {
		return &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
				CalendarDate: &presentation.CalendarDate{
					CalendarYear:        int32(t.Year()),
					CalendarMonthNumber: int32(t.Month()),
					CalendarDayOfMonth:  int32(t.Day()),
				},
			},
		}
	}
	out := &presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
			ExactTime: timestamppb.New(t),
		},
	}
	if c.utcOffsetSeconds != 0 {
		out.EventUtcOffsetSecondsRecordedBySourceArchive = proto.Int32(int32(c.utcOffsetSeconds))
	}
	return out
}

func buildFTS5QueryExpression(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	expanded := ExpandEastAsianBigrams(trimmed)
	tokens := strings.Fields(expanded)
	if len(tokens) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		clean := strings.Trim(tok, `"'`)
		clean = strings.ReplaceAll(clean, `"`, "")
		if clean == "" {
			continue
		}
		quoted = append(quoted, `"`+clean+`"`)
	}
	return strings.Join(quoted, " ")
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// ReadUnifiedIndexStatus inspects <stateRoot>/index.db read-only.
func ReadUnifiedIndexStatus(ctx context.Context, stateRoot string) (UnifiedIndexStatus, error) {
	indexPath := ResolveIndexDatabasePath(stateRoot)
	info, err := os.Stat(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return UnifiedIndexStatus{IndexPath: indexPath, Exists: false}, nil
	}
	if err != nil {
		return UnifiedIndexStatus{}, err
	}
	st, err := OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
	if err != nil {
		return UnifiedIndexStatus{}, err
	}
	if st == nil {
		return UnifiedIndexStatus{IndexPath: indexPath, Exists: false}, nil
	}
	defer func() { _ = st.Close() }()

	status := UnifiedIndexStatus{
		IndexPath:         indexPath,
		Exists:            true,
		DatabaseByteCount: uint64(info.Size()),
	}
	var updatedAt string
	if err := st.DB().QueryRowContext(ctx, `select value from indexed_archive_metadata where key = 'updated_at_utc'`).Scan(&updatedAt); err == nil {
		if parsed, parseErr := time.Parse(time.RFC3339, updatedAt); parseErr == nil {
			status.UpdatedAtUTC = parsed
		}
	}
	_ = st.DB().QueryRowContext(ctx, `select count(*) from indexed_archive_records`).Scan(&status.TotalIndexedCount)
	_ = st.DB().QueryRowContext(ctx, `select count(*) from indexed_record_anchors`).Scan(&status.AnchorCount)
	_ = st.DB().QueryRowContext(ctx, `select count(*) from indexed_money_movements`).Scan(&status.MoneyMovementCount)
	_ = st.DB().QueryRowContext(ctx, `select count(*) from indexed_time_windows`).Scan(&status.TimeWindowCount)

	rows, err := st.DB().QueryContext(ctx, `
		select trawler, trawler_display_name, record_count, index_cursor, indexed_at_utc
		from indexed_trawler_state
		where indexed_at_utc <> ''
		order by record_count desc, trawler asc
	`)
	if err != nil {
		return status, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item IndexedTrawlerStatus
		var indexedAt string
		if err := rows.Scan(&item.TrawlerIdentity, &item.TrawlerDisplayName, &item.RecordCount, &item.IndexCursor, &indexedAt); err != nil {
			return status, err
		}
		if parsed, parseErr := time.Parse(time.RFC3339, indexedAt); parseErr == nil {
			item.IndexedAtUTC = parsed
		}
		status.Trawlers = append(status.Trawlers, item)
	}
	return status, rows.Err()
}
