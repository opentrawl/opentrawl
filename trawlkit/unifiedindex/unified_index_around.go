package unifiedindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
)

// AroundQuery specifies a cross-plugin join anchor: a trawl link, a calendar
// date (YYYY-MM-DD), or an indexed time window (--trip).
type AroundQuery struct {
	Target        string
	TripQuery     string
	Window        time.Duration
	Limit         int
	TrawlerFilter []string
}

// AroundMatch describes one record joined to the requested anchor.
type AroundMatch struct {
	DocID            int64
	Trawler          string
	TrawlerDisplay   string
	RecordKind       string
	Link             string
	OccurredUnix     int64
	HasTimeOfDay     bool
	UTCOffsetSeconds int
	Title            string
	PeopleText       string
	BodyPreview      string
	MoneyDecimal     string
	MoneyCurrency    string
	MoneyPending     bool
	JoinReason       string
	JoinScore        float64
}

// AroundResult holds the resolved anchor and ranked cross-plugin matches.
type AroundResult struct {
	AnchorSummary string
	AnchorLink    string
	WindowStart   time.Time
	WindowEnd     time.Time
	Matches       []AroundMatch
	TotalFound    int
	Elapsed       time.Duration
}

type anchorRecordRow struct {
	docID            int64
	trawler          string
	trawlerDisplay   string
	recordKind       string
	canonicalRef     string
	resolvedRef      string
	link             string
	occurredUnix     sql.NullInt64
	hasTimeOfDay     int
	utcOffsetSeconds int
	title            string
	peopleText       string
	bodyText         string
	metaText         string
	moneyDecimal     string
	moneyCurrency    string
	moneyPending     int
}

// FindAround executes mechanical cross-plugin joins over core index.db.
func FindAround(ctx context.Context, db *sql.DB, q AroundQuery) (AroundResult, error) {
	started := time.Now()
	if q.Window <= 0 {
		q.Window = 3 * 24 * time.Hour
	}
	if q.Limit <= 0 {
		q.Limit = 30
	}
	if strings.TrimSpace(q.TripQuery) != "" {
		return findAroundTimeWindow(ctx, db, q, started)
	}
	target := strings.TrimSpace(q.Target)
	if target == "" {
		return AroundResult{}, errors.New("around requires a trawl link, date (YYYY-MM-DD), or --trip")
	}
	if parsedDate, err := time.Parse("2006-01-02", target); err == nil {
		return findAroundDate(ctx, db, parsedDate, q, started)
	}
	return findAroundRecordLink(ctx, db, target, q, started)
}

func findAroundTimeWindow(ctx context.Context, db *sql.DB, q AroundQuery, started time.Time) (AroundResult, error) {
	like := "%" + strings.ToLower(strings.TrimSpace(q.TripQuery)) + "%"
	var winID, placeTitle, dateTitle string
	var startUnix, endUnix int64
	err := db.QueryRowContext(ctx, `
		select window_id, place_title, date_title, start_unix, end_unix
		from indexed_time_windows
		where lower(place_title) like ? or lower(date_title) like ?
		order by start_unix desc
		limit 1
	`, like, like).Scan(&winID, &placeTitle, &dateTitle, &startUnix, &endUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return AroundResult{}, fmt.Errorf("no indexed trip matched %q", q.TripQuery)
	}
	if err != nil {
		return AroundResult{}, err
	}
	pad := int64(24 * time.Hour / time.Second)
	matches, err := fetchWindowMatches(ctx, db, startUnix-pad, endUnix+pad, 0, 0, q.TrawlerFilter, q.Limit, "during trip")
	if err != nil {
		return AroundResult{}, err
	}
	label := strings.TrimSpace(strings.Join(nonEmptyStrings(placeTitle, dateTitle), " · "))
	if label == "" {
		label = winID
	}
	return AroundResult{
		AnchorSummary: "Trip: " + label,
		WindowStart:   time.Unix(startUnix, 0).UTC(),
		WindowEnd:     time.Unix(endUnix, 0).UTC(),
		Matches:       matches,
		TotalFound:    len(matches),
		Elapsed:       time.Since(started),
	}, nil
}

func findAroundDate(ctx context.Context, db *sql.DB, date time.Time, q AroundQuery, started time.Time) (AroundResult, error) {
	center := date.UTC().Add(12 * time.Hour)
	start := center.Add(-q.Window).Unix()
	end := center.Add(q.Window).Unix()
	matches, err := fetchWindowMatches(ctx, db, start, end, center.Unix(), 0, q.TrawlerFilter, q.Limit, "within window")
	if err != nil {
		return AroundResult{}, err
	}
	return AroundResult{
		AnchorSummary: "Date: " + date.Format("2006-01-02"),
		WindowStart:   time.Unix(start, 0).UTC(),
		WindowEnd:     time.Unix(end, 0).UTC(),
		Matches:       matches,
		TotalFound:    len(matches),
		Elapsed:       time.Since(started),
	}, nil
}

func findAroundRecordLink(ctx context.Context, db *sql.DB, targetLink string, q AroundQuery, started time.Time) (AroundResult, error) {
	anchor, err := loadAnchorRecordByLink(ctx, db, targetLink)
	if err != nil {
		return AroundResult{}, err
	}
	centerUnix := anchor.occurredUnix.Int64
	winSec := int64(q.Window / time.Second)
	startUnix := centerUnix - winSec
	endUnix := centerUnix + winSec

	byDoc := make(map[int64]AroundMatch, 64)
	addOrBoost := func(m AroundMatch) {
		if m.DocID == anchor.docID {
			return
		}
		if existing, ok := byDoc[m.DocID]; ok {
			if m.JoinScore > existing.JoinScore {
				existing.JoinScore = m.JoinScore
				existing.JoinReason = m.JoinReason
				byDoc[m.DocID] = existing
			}
			return
		}
		byDoc[m.DocID] = m
	}

	if err := collectExactAnchorJoins(ctx, db, anchor, q.TrawlerFilter, addOrBoost); err != nil {
		return AroundResult{}, err
	}
	if anchor.trawler == "money" {
		if err := collectMoneyReceiptJoins(ctx, db, anchor, q.TrawlerFilter, addOrBoost); err != nil {
			return AroundResult{}, err
		}
	}
	if anchor.occurredUnix.Valid && anchor.occurredUnix.Int64 > 0 {
		winMatches, err := fetchWindowMatches(ctx, db, startUnix, endUnix, centerUnix, anchor.docID, q.TrawlerFilter, q.Limit*2, "same time window")
		if err != nil {
			return AroundResult{}, err
		}
		anchorTokens := TokenizeSearchWords(anchor.title + " " + anchor.peopleText)
		for _, m := range winMatches {
			deltaHours := math.Abs(float64(m.OccurredUnix-centerUnix)) / 3600.0
			m.JoinScore = 10.0 / (1.0 + deltaHours/24.0)
			if len(anchorTokens) > 0 && m.Trawler != anchor.trawler {
				haystack := strings.ToLower(m.Title + " " + m.PeopleText + " " + m.BodyPreview)
				overlap := 0
				for _, tok := range anchorTokens {
					if len(tok) >= 4 && strings.Contains(haystack, tok) {
						overlap++
					}
				}
				if overlap > 0 {
					m.JoinScore += 35.0 + float64(overlap)*8.0
					m.JoinReason = "same time window + matching terms"
				}
			}
			addOrBoost(m)
		}
	}

	ranked := make([]AroundMatch, 0, len(byDoc))
	for _, m := range byDoc {
		ranked = append(ranked, m)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].JoinScore != ranked[j].JoinScore {
			return ranked[i].JoinScore > ranked[j].JoinScore
		}
		return ranked[i].OccurredUnix > ranked[j].OccurredUnix
	})
	totalFound := len(ranked)
	if len(ranked) > q.Limit {
		ranked = ranked[:q.Limit]
	}

	summary := strings.TrimSpace(anchor.title)
	if anchor.moneyDecimal != "" {
		summary = strings.TrimSpace(fmt.Sprintf("%s %s %s", anchor.moneyDecimal, anchor.moneyCurrency, summary))
	}
	return AroundResult{
		AnchorSummary: fmt.Sprintf("%s (%s)", summary, anchor.trawlerDisplay),
		AnchorLink:    anchor.link,
		WindowStart:   time.Unix(startUnix, 0).UTC(),
		WindowEnd:     time.Unix(endUnix, 0).UTC(),
		Matches:       ranked,
		TotalFound:    totalFound,
		Elapsed:       time.Since(started),
	}, nil
}

func loadAnchorRecordByLink(ctx context.Context, db *sql.DB, rawLink string) (anchorRecordRow, error) {
	trawler, alias, ok := strings.Cut(strings.TrimSpace(rawLink), ":")
	if !ok || strings.TrimSpace(trawler) == "" || strings.TrimSpace(alias) == "" {
		return anchorRecordRow{}, fmt.Errorf("invalid link %q (expected trawler:alias)", rawLink)
	}
	trawler = strings.ToLower(strings.TrimSpace(trawler))
	alias = strings.TrimSpace(alias)

	resolvedRefs, err := shortref.NewScopedSQLiteIndex(db, trawler).Lookup(ctx, alias)
	if err != nil {
		return anchorRecordRow{}, err
	}
	if len(resolvedRefs) == 0 {
		return anchorRecordRow{}, fmt.Errorf("link %q not found in unified index", rawLink)
	}
	if len(resolvedRefs) > 1 {
		return anchorRecordRow{}, fmt.Errorf("link %q is ambiguous (%d matches)", rawLink, len(resolvedRefs))
	}
	resolvedRef := resolvedRefs[0]

	var row anchorRecordRow
	err = db.QueryRowContext(ctx, `
		select doc_id, trawler, trawler_display_name, record_kind, canonical_ref, resolved_ref,
		       occurred_unix, has_time_of_day, utc_offset_seconds,
		       title, people_text, body_text, meta_text,
		       money_decimal_amount, money_currency_code, money_is_pending
		from indexed_archive_records
		where trawler = ? and (resolved_ref = ? or canonical_ref = ?)
		limit 1
	`, trawler, resolvedRef, resolvedRef).Scan(
		&row.docID, &row.trawler, &row.trawlerDisplay, &row.recordKind, &row.canonicalRef, &row.resolvedRef,
		&row.occurredUnix, &row.hasTimeOfDay, &row.utcOffsetSeconds,
		&row.title, &row.peopleText, &row.bodyText, &row.metaText,
		&row.moneyDecimal, &row.moneyCurrency, &row.moneyPending,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return anchorRecordRow{}, fmt.Errorf("record for %q is not indexed in index.db", rawLink)
	}
	if err != nil {
		return anchorRecordRow{}, err
	}
	row.link = trawler + ":" + alias
	return row, nil
}

func collectExactAnchorJoins(
	ctx context.Context,
	db *sql.DB,
	anchor anchorRecordRow,
	trawlerFilter []string,
	add func(AroundMatch),
) error {
	rows, err := db.QueryContext(ctx, `
		select a2.anchor_kind, r.doc_id, r.trawler, r.trawler_display_name, r.record_kind,
		       r.canonical_ref, r.resolved_ref, r.occurred_unix, r.has_time_of_day, r.utc_offset_seconds,
		       r.title, r.people_text, r.body_text, r.money_decimal_amount, r.money_currency_code, r.money_is_pending
		from indexed_record_anchors a1
		join indexed_record_anchors a2
		  on a2.anchor_kind = a1.anchor_kind and a2.anchor_value = a1.anchor_value and a2.doc_id <> a1.doc_id
		join indexed_archive_records r on r.doc_id = a2.doc_id
		where a1.doc_id = ?
		  and a1.anchor_kind in ('rfc_msg_id', 'booking_code', 'amount_minor')
		limit 80
	`, anchor.docID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type rawHit struct {
		kind        string
		resolvedRef string
		match       AroundMatch
	}
	var hits []rawHit
	for rows.Next() {
		var kind, canonicalRef, resolvedRef string
		var occurred sql.NullInt64
		var hasTOD, utcOff, pending int
		var m AroundMatch
		if err := rows.Scan(
			&kind, &m.DocID, &m.Trawler, &m.TrawlerDisplay, &m.RecordKind,
			&canonicalRef, &resolvedRef, &occurred, &hasTOD, &utcOff,
			&m.Title, &m.PeopleText, &m.BodyPreview, &m.MoneyDecimal, &m.MoneyCurrency, &pending,
		); err != nil {
			return err
		}
		if len(trawlerFilter) > 0 && !containsString(trawlerFilter, m.Trawler) {
			continue
		}
		if anchor.occurredUnix.Valid && occurred.Valid && kind == "amount_minor" {
			deltaDays := math.Abs(float64(occurred.Int64-anchor.occurredUnix.Int64)) / 86400.0
			if deltaDays > 14.0 {
				continue
			}
		}
		m.OccurredUnix = occurred.Int64
		m.HasTimeOfDay = hasTOD != 0
		m.UTCOffsetSeconds = utcOff
		m.MoneyPending = pending != 0
		m.BodyPreview = TruncateRunes(m.BodyPreview, 160)
		switch kind {
		case "rfc_msg_id":
			m.JoinReason = "same email Message-ID"
			m.JoinScore = 100.0
		case "booking_code":
			m.JoinReason = "same booking code"
			m.JoinScore = 95.0
		case "amount_minor":
			m.JoinReason = "exact amount match"
			m.JoinScore = 80.0
		default:
			m.JoinReason = "shared anchor"
			m.JoinScore = 50.0
		}
		hits = append(hits, rawHit{kind: kind, resolvedRef: resolvedRef, match: m})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()
	links, err := resolveAroundLinks(ctx, db, func(yield func(string, string)) {
		for _, h := range hits {
			yield(h.match.Trawler, h.resolvedRef)
		}
	})
	if err != nil {
		return err
	}
	for _, h := range hits {
		h.match.Link = links[h.match.Trawler+"\x00"+h.resolvedRef]
		add(h.match)
	}
	return nil
}

func collectMoneyReceiptJoins(
	ctx context.Context,
	db *sql.DB,
	anchor anchorRecordRow,
	trawlerFilter []string,
	add func(AroundMatch),
) error {
	var absMinor, origAbsMinor sql.NullInt64
	var counterparty string
	err := db.QueryRowContext(ctx, `
		select abs_minor_units, orig_abs_minor_units, counterparty
		from indexed_money_movements where doc_id = ?
	`, anchor.docID).Scan(&absMinor, &origAbsMinor, &counterparty)
	if err != nil {
		return nil
	}
	var amounts []string
	if absMinor.Valid && absMinor.Int64 > 0 {
		amounts = append(amounts, strconv.FormatInt(absMinor.Int64, 10))
	}
	if origAbsMinor.Valid && origAbsMinor.Int64 > 0 {
		amounts = append(amounts, strconv.FormatInt(origAbsMinor.Int64, 10))
	}
	if len(amounts) == 0 || !anchor.occurredUnix.Valid {
		return nil
	}
	counterTokens := TokenizeSearchWords(counterparty)
	windowStart := anchor.occurredUnix.Int64 - int64(14*24*time.Hour/time.Second)
	windowEnd := anchor.occurredUnix.Int64 + int64(14*24*time.Hour/time.Second)

	ph := strings.TrimSuffix(strings.Repeat("?,", len(amounts)), ",")
	args := make([]any, 0, len(amounts)+3)
	for _, a := range amounts {
		args = append(args, a)
	}
	args = append(args, windowStart, windowEnd, anchor.docID)

	rows, err := db.QueryContext(ctx, `
		select distinct r.doc_id, r.trawler, r.trawler_display_name, r.record_kind,
		       r.resolved_ref, r.occurred_unix, r.has_time_of_day, r.utc_offset_seconds,
		       r.title, r.people_text, r.body_text, r.meta_text,
		       r.money_decimal_amount, r.money_currency_code, r.money_is_pending
		from indexed_record_anchors a
		join indexed_archive_records r on r.doc_id = a.doc_id
		where a.anchor_kind = 'amount_minor'
		  and a.anchor_value in (`+ph+`)
		  and r.occurred_unix between ? and ?
		  and r.doc_id <> ?
		  and r.trawler <> 'money'
		limit 40
	`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type rawHit struct {
		resolvedRef string
		match       AroundMatch
	}
	var hits []rawHit
	for rows.Next() {
		var resolvedRef, meta string
		var occurred sql.NullInt64
		var hasTOD, utcOff, pending int
		var m AroundMatch
		if err := rows.Scan(
			&m.DocID, &m.Trawler, &m.TrawlerDisplay, &m.RecordKind,
			&resolvedRef, &occurred, &hasTOD, &utcOff,
			&m.Title, &m.PeopleText, &m.BodyPreview, &meta,
			&m.MoneyDecimal, &m.MoneyCurrency, &pending,
		); err != nil {
			return err
		}
		if len(trawlerFilter) > 0 && !containsString(trawlerFilter, m.Trawler) {
			continue
		}
		haystack := strings.ToLower(m.Title + " " + m.PeopleText + " " + meta + " " + m.BodyPreview)
		tokenOverlap := 0
		for _, tok := range counterTokens {
			if len(tok) >= 3 && strings.Contains(haystack, tok) {
				tokenOverlap++
			}
		}
		m.OccurredUnix = occurred.Int64
		m.HasTimeOfDay = hasTOD != 0
		m.UTCOffsetSeconds = utcOff
		m.MoneyPending = pending != 0
		m.BodyPreview = TruncateRunes(m.BodyPreview, 160)
		if tokenOverlap > 0 {
			m.JoinReason = "exact amount + merchant match"
			m.JoinScore = 95.0 + float64(tokenOverlap)*5.0
		} else {
			m.JoinReason = "exact amount within 14d"
			m.JoinScore = 70.0
		}
		hits = append(hits, rawHit{resolvedRef: resolvedRef, match: m})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()
	links, err := resolveAroundLinks(ctx, db, func(yield func(string, string)) {
		for _, h := range hits {
			yield(h.match.Trawler, h.resolvedRef)
		}
	})
	if err != nil {
		return err
	}
	for _, h := range hits {
		h.match.Link = links[h.match.Trawler+"\x00"+h.resolvedRef]
		add(h.match)
	}
	return nil
}

func fetchWindowMatches(
	ctx context.Context,
	db *sql.DB,
	startUnix int64,
	endUnix int64,
	centerUnix int64,
	excludeDocID int64,
	trawlerFilter []string,
	limit int,
	reason string,
) ([]AroundMatch, error) {
	args := []any{startUnix, endUnix, excludeDocID}
	filterSQL := ""
	if len(trawlerFilter) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(trawlerFilter)), ",")
		filterSQL = ` and trawler in (` + ph + `)`
		for _, t := range trawlerFilter {
			args = append(args, t)
		}
	}
	orderSQL := `order by occurred_unix desc`
	if centerUnix > 0 {
		orderSQL = `order by abs(occurred_unix - ?) asc, occurred_unix desc`
		args = append(args, centerUnix)
	}
	args = append(args, max(limit*3, 120))
	rows, err := db.QueryContext(ctx, `
		select doc_id, trawler, trawler_display_name, record_kind, resolved_ref,
		       occurred_unix, has_time_of_day, utc_offset_seconds,
		       title, people_text, body_text,
		       money_decimal_amount, money_currency_code, money_is_pending
		from indexed_archive_records
		where occurred_unix between ? and ?
		  and doc_id <> ?
		  and source_role not in ('like', 'other')`+filterSQL+`
		`+orderSQL+`
		limit ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type rawHit struct {
		resolvedRef string
		match       AroundMatch
	}
	var hits []rawHit
	perTrawler := make(map[string]int, 10)
	capPerTrawler := max(limit/3, 4)
	for rows.Next() {
		var resolvedRef string
		var occurred sql.NullInt64
		var hasTOD, utcOff, pending int
		var m AroundMatch
		if err := rows.Scan(
			&m.DocID, &m.Trawler, &m.TrawlerDisplay, &m.RecordKind, &resolvedRef,
			&occurred, &hasTOD, &utcOff,
			&m.Title, &m.PeopleText, &m.BodyPreview,
			&m.MoneyDecimal, &m.MoneyCurrency, &pending,
		); err != nil {
			return nil, err
		}
		if len(trawlerFilter) != 1 && perTrawler[m.Trawler] >= capPerTrawler {
			continue
		}
		perTrawler[m.Trawler]++
		m.OccurredUnix = occurred.Int64
		m.HasTimeOfDay = hasTOD != 0
		m.UTCOffsetSeconds = utcOff
		m.MoneyPending = pending != 0
		m.BodyPreview = TruncateRunes(m.BodyPreview, 160)
		m.JoinReason = reason
		m.JoinScore = 5.0
		hits = append(hits, rawHit{resolvedRef: resolvedRef, match: m})
		if len(hits) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	links, err := resolveAroundLinks(ctx, db, func(yield func(string, string)) {
		for _, h := range hits {
			yield(h.match.Trawler, h.resolvedRef)
		}
	})
	if err != nil {
		return nil, err
	}
	out := make([]AroundMatch, 0, len(hits))
	for _, h := range hits {
		h.match.Link = links[h.match.Trawler+"\x00"+h.resolvedRef]
		out = append(out, h.match)
	}
	return out, nil
}

func resolveAroundLinks(
	ctx context.Context,
	db *sql.DB,
	iter func(yield func(trawler, resolvedRef string)),
) (map[string]string, error) {
	byTrawler := make(map[string][]string, 8)
	iter(func(trawler, resolvedRef string) {
		byTrawler[trawler] = append(byTrawler[trawler], resolvedRef)
	})
	links := make(map[string]string)
	for trawler, refs := range byTrawler {
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
