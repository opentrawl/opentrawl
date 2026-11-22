package unifiedindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

var indexWriteMutex sync.Mutex

// OpenUnifiedIndexReadWrite opens <stateRoot>/index.db creating its schema if needed.
func OpenUnifiedIndexReadWrite(ctx context.Context, stateRoot string) (*store.Store, error) {
	indexPath := ResolveIndexDatabasePath(stateRoot)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return nil, fmt.Errorf("create unified index directory: %w", err)
	}
	st, err := store.Open(ctx, store.Options{Path: indexPath})
	if err != nil {
		return nil, fmt.Errorf("open unified index read-write: %w", err)
	}
	if err := EnsureUnifiedIndexSchema(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

// OpenUnifiedIndexReadOnly opens <stateRoot>/index.db read-only if present.
// Returns (nil, nil) when the file does not exist yet.
func OpenUnifiedIndexReadOnly(ctx context.Context, stateRoot string, writesNoFiles bool) (*store.Store, error) {
	indexPath := ResolveIndexDatabasePath(stateRoot)
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat unified index: %w", err)
	}
	if writesNoFiles {
		st, err := store.OpenReadOnlyWithoutWritingFiles(ctx, indexPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return st, err
	}
	st, err := store.OpenReadOnly(ctx, indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return st, err
}

// EnsureTrawlerShortReferencesMigratedVerbatim copies any existing short
// references (including tombstones) from Wave B's <stateRoot>/shortrefs/<trawler>.db
// or legacy <legacyArchivePath> verbatim into index.db on first access.
func EnsureTrawlerShortReferencesMigratedVerbatim(
	ctx context.Context,
	db *sql.DB,
	stateRoot string,
	trawler string,
	legacyArchivePath string,
) error {
	trawler = strings.TrimSpace(trawler)
	if trawler == "" {
		return nil
	}
	var migrated int
	err := db.QueryRowContext(
		ctx,
		`select shortrefs_migrated from indexed_trawler_state where trawler = ?`,
		trawler,
	).Scan(&migrated)
	if err == nil && migrated == 1 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check short reference migration state for %s: %w", trawler, err)
	}

	indexWriteMutex.Lock()
	defer indexWriteMutex.Unlock()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin short reference migration for %s: %w", trawler, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := migrateShortReferencesInsideTx(ctx, tx, stateRoot, trawler, legacyArchivePath); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateShortReferencesInsideTx(
	ctx context.Context,
	tx *sql.Tx,
	stateRoot string,
	trawler string,
	legacyArchivePath string,
) error {
	var migrated int
	err := tx.QueryRowContext(
		ctx,
		`select shortrefs_migrated from indexed_trawler_state where trawler = ?`,
		trawler,
	).Scan(&migrated)
	if err == nil && migrated == 1 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	sources := []string{
		filepath.Join(stateRoot, "shortrefs", trawler+".db"),
		strings.TrimSpace(legacyArchivePath),
	}
	for _, srcPath := range sources {
		copied, copyErr := copyShortRefRowsFromSQLiteSource(ctx, tx, trawler, srcPath)
		if copyErr != nil {
			return copyErr
		}
		if copied > 0 {
			break
		}
	}
	_, err = tx.ExecContext(ctx, `
insert into indexed_trawler_state(trawler, shortrefs_migrated)
values (?, 1)
on conflict(trawler) do update set shortrefs_migrated = 1
`, trawler)
	return err
}

func copyShortRefRowsFromSQLiteSource(
	ctx context.Context,
	tx *sql.Tx,
	trawler string,
	sourcePath string,
) (int, error) {
	if sourcePath == "" || sourcePath == ":memory:" || strings.HasPrefix(sourcePath, "file:") {
		return 0, nil
	}
	if _, err := os.Stat(sourcePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat short reference migration source %s: %w", sourcePath, err)
	}
	srcStore, err := store.OpenReadOnlyWithoutWritingFiles(ctx, sourcePath)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = srcStore.Close() }()

	var hasTable int
	if err := srcStore.DB().QueryRowContext(
		ctx,
		`select 1 from sqlite_master where type = 'table' and name = 'short_refs'`,
	).Scan(&hasTable); err != nil {
		return 0, nil
	}
	hasTombstoned := sqliteTableHasColumn(ctx, srcStore.DB(), "short_refs", "tombstoned")
	query := `select alias, full_ref, canonical_ref, 0 from short_refs`
	if hasTombstoned {
		query = `select alias, full_ref, canonical_ref, tombstoned from short_refs`
	}
	rows, err := srcStore.DB().QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("read legacy short refs from %s: %w", sourcePath, err)
	}
	defer func() { _ = rows.Close() }()

	stmt, err := tx.PrepareContext(ctx, `
insert or ignore into trawler_short_references(trawler, alias, full_ref, canonical_ref, tombstoned)
values (?, ?, ?, ?, ?)
`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()

	count := 0
	for rows.Next() {
		var alias, fullRef, canonicalRef string
		var tombstoned int
		if err := rows.Scan(&alias, &fullRef, &canonicalRef, &tombstoned); err != nil {
			return count, fmt.Errorf("scan legacy short ref: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, trawler, alias, fullRef, canonicalRef, tombstoned); err != nil {
			return count, fmt.Errorf("insert migrated short ref: %w", err)
		}
		count++
	}
	return count, rows.Err()
}

func sqliteTableHasColumn(ctx context.Context, db *sql.DB, tableName, colName string) bool {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+tableName+`)`)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err == nil && name == colName {
			return true
		}
	}
	return false
}

// ReadTrawlerIndexCursor returns the last committed opaque cursor for trawler.
func ReadTrawlerIndexCursor(ctx context.Context, db *sql.DB, trawler string) (string, bool, error) {
	var cursor string
	var indexedAt string
	err := db.QueryRowContext(
		ctx,
		`select index_cursor, indexed_at_utc from indexed_trawler_state where trawler = ?`,
		strings.TrimSpace(trawler),
	).Scan(&cursor, &indexedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return cursor, indexedAt != "", nil
}

// CommitTrawlerIndexChangeBatches atomically commits short references,
// searchable records (with FTS5 sync triggers), anchors, money movements,
// time windows, and the next opaque cursor for one trawler.
func CommitTrawlerIndexChangeBatches(
	ctx context.Context,
	db *sql.DB,
	stateRoot string,
	trawler string,
	trawlerDisplayName string,
	legacyArchivePath string,
	batches []*indexcontract.ArchiveIndexChangeBatch,
) error {
	trawler = strings.TrimSpace(trawler)
	if trawler == "" {
		return errors.New("trawler identity is required to commit index changes")
	}
	if len(batches) == 0 {
		return nil
	}

	indexWriteMutex.Lock()
	defer indexWriteMutex.Unlock()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unified index commit for %s: %w", trawler, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := migrateShortReferencesInsideTx(ctx, tx, stateRoot, trawler, legacyArchivePath); err != nil {
		return err
	}

	completeSnapshot := false
	nextCursor := ""
	var allShortRefCandidates []shortref.ScopedShortReferenceCandidate
	var allDeletedRefs []string
	for _, batch := range batches {
		if batch == nil {
			continue
		}
		if batch.GetCompleteSnapshotReplacement() {
			completeSnapshot = true
		}
		if c := strings.TrimSpace(batch.GetNextOpaqueCursor()); c != "" {
			nextCursor = c
		}
		for _, upsert := range batch.GetUpserts() {
			stable := canonicalRefString(upsert.GetStableRecordReference())
			if stable == "" {
				continue
			}
			resolved := canonicalRefString(upsert.GetResolvedRecordReference())
			if resolved == "" {
				resolved = stable
			}
			allShortRefCandidates = append(allShortRefCandidates, shortref.ScopedShortReferenceCandidate{
				StableFullRef: stable,
				CanonicalRef:  resolved,
			})
		}
		for _, extra := range batch.GetShortReferenceOnlyEntries() {
			stable := canonicalRefString(extra.GetStableRecordReference())
			if stable == "" {
				continue
			}
			resolved := canonicalRefString(extra.GetResolvedRecordReference())
			if resolved == "" {
				resolved = stable
			}
			allShortRefCandidates = append(allShortRefCandidates, shortref.ScopedShortReferenceCandidate{
				StableFullRef: stable,
				CanonicalRef:  resolved,
			})
		}
		for _, del := range batch.GetDeletedRecordReferences() {
			if ref := canonicalRefString(del); ref != "" {
				allDeletedRefs = append(allDeletedRefs, ref)
			}
		}
	}

	scopedShortRefs := shortref.NewScopedSQLiteIndex(tx, trawler)
	if err := scopedShortRefs.AssignCandidates(ctx, allShortRefCandidates, completeSnapshot, allDeletedRefs); err != nil {
		return fmt.Errorf("commit short refs for %s: %w", trawler, err)
	}

	if completeSnapshot {
		if err := clearTrawlerIndexedRows(ctx, tx, trawler); err != nil {
			return err
		}
	} else if len(allDeletedRefs) > 0 {
		if err := deleteTrawlerIndexedRecords(ctx, tx, trawler, allDeletedRefs); err != nil {
			return err
		}
	}

	if err := applyUpsertsAndWindowsInsideTx(ctx, tx, trawler, trawlerDisplayName, batches); err != nil {
		return err
	}

	var count int64
	if err := tx.QueryRowContext(
		ctx,
		`select count(*) from indexed_archive_records where trawler = ?`,
		trawler,
	).Scan(&count); err != nil {
		return fmt.Errorf("count indexed records for %s: %w", trawler, err)
	}

	nowUTC := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
insert into indexed_trawler_state(trawler, trawler_display_name, shortrefs_migrated, index_cursor, indexed_at_utc, record_count)
values (?, ?, 1, ?, ?, ?)
on conflict(trawler) do update set
  trawler_display_name = excluded.trawler_display_name,
  shortrefs_migrated = 1,
  index_cursor = excluded.index_cursor,
  indexed_at_utc = excluded.indexed_at_utc,
  record_count = excluded.record_count
`, trawler, trawlerDisplayName, nextCursor, nowUTC, count); err != nil {
		return fmt.Errorf("update indexed trawler state for %s: %w", trawler, err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into indexed_archive_metadata(key, value) values ('updated_at_utc', ?)
on conflict(key) do update set value = excluded.value
`, nowUTC); err != nil {
		return fmt.Errorf("update unified index metadata: %w", err)
	}
	return tx.Commit()
}

func clearTrawlerIndexedRows(ctx context.Context, tx *sql.Tx, trawler string) error {
	stmts := []string{
		`delete from indexed_record_anchors where doc_id in (select doc_id from indexed_archive_records where trawler = ?)`,
		`delete from indexed_money_movements where trawler = ?`,
		`delete from indexed_time_windows where trawler = ?`,
		`delete from indexed_archive_records where trawler = ?`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, trawler); err != nil {
			return fmt.Errorf("clear trawler %s indexed rows: %w", trawler, err)
		}
	}
	return nil
}

func deleteTrawlerIndexedRecords(ctx context.Context, tx *sql.Tx, trawler string, refs []string) error {
	for start := 0; start < len(refs); start += 450 {
		end := min(start+450, len(refs))
		chunk := refs[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, trawler)
		for _, r := range chunk {
			args = append(args, r)
		}
		if _, err := tx.ExecContext(ctx, `
delete from indexed_record_anchors
where doc_id in (
  select doc_id from indexed_archive_records where trawler = ? and canonical_ref in (`+ph+`)
)`, args...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
delete from indexed_money_movements where trawler = ? and canonical_ref in (`+ph+`)
`, args...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
delete from indexed_archive_records where trawler = ? and canonical_ref in (`+ph+`)
`, args...); err != nil {
			return err
		}
	}
	return nil
}

func applyUpsertsAndWindowsInsideTx(
	ctx context.Context,
	tx *sql.Tx,
	trawler string,
	trawlerDisplayName string,
	batches []*indexcontract.ArchiveIndexChangeBatch,
) error {
	upsertStmt, err := tx.PrepareContext(ctx, `
insert into indexed_archive_records(
  trawler, trawler_display_name, record_kind, source_role,
  canonical_ref, resolved_ref, record_anchor, conversation_key, display_context,
  occurred_unix, has_time_of_day, utc_offset_seconds, end_unix,
  title, people_text, body_text, meta_text, observed_labels_text, status_labels_text,
  money_decimal_amount, money_currency_code, money_is_pending
) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
on conflict(trawler, canonical_ref) do update set
  trawler_display_name = excluded.trawler_display_name,
  record_kind = excluded.record_kind,
  source_role = excluded.source_role,
  resolved_ref = excluded.resolved_ref,
  record_anchor = excluded.record_anchor,
  conversation_key = excluded.conversation_key,
  display_context = excluded.display_context,
  occurred_unix = excluded.occurred_unix,
  has_time_of_day = excluded.has_time_of_day,
  utc_offset_seconds = excluded.utc_offset_seconds,
  end_unix = excluded.end_unix,
  title = excluded.title,
  people_text = excluded.people_text,
  body_text = excluded.body_text,
  meta_text = excluded.meta_text,
  observed_labels_text = excluded.observed_labels_text,
  status_labels_text = excluded.status_labels_text,
  money_decimal_amount = excluded.money_decimal_amount,
  money_currency_code = excluded.money_currency_code,
  money_is_pending = excluded.money_is_pending
returning doc_id
`)
	if err != nil {
		return err
	}
	defer func() { _ = upsertStmt.Close() }()

	delAnchorsStmt, err := tx.PrepareContext(ctx, `delete from indexed_record_anchors where doc_id = ?`)
	if err != nil {
		return err
	}
	defer func() { _ = delAnchorsStmt.Close() }()

	insAnchorStmt, err := tx.PrepareContext(ctx, `insert into indexed_record_anchors(doc_id, anchor_kind, anchor_value) values (?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insAnchorStmt.Close() }()

	insMoneyStmt, err := tx.PrepareContext(ctx, `
insert into indexed_money_movements(
  doc_id, trawler, canonical_ref, booking_date, occurred_unix,
  signed_minor_units, abs_minor_units, currency_code,
  orig_abs_minor_units, orig_currency_code,
  counterparty, description, merchant_city, merchant_country, pending
) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
on conflict(doc_id) do update set
  booking_date = excluded.booking_date,
  occurred_unix = excluded.occurred_unix,
  signed_minor_units = excluded.signed_minor_units,
  abs_minor_units = excluded.abs_minor_units,
  currency_code = excluded.currency_code,
  orig_abs_minor_units = excluded.orig_abs_minor_units,
  orig_currency_code = excluded.orig_currency_code,
  counterparty = excluded.counterparty,
  description = excluded.description,
  merchant_city = excluded.merchant_city,
  merchant_country = excluded.merchant_country,
  pending = excluded.pending
`)
	if err != nil {
		return err
	}
	defer func() { _ = insMoneyStmt.Close() }()

	insWindowStmt, err := tx.PrepareContext(ctx, `
insert into indexed_time_windows(
  window_id, trawler, window_kind, place_title, date_title, start_unix, end_unix, related_canonical_ref
) values (?,?,?,?,?,?,?,?)
on conflict(window_id) do update set
  place_title = excluded.place_title,
  date_title = excluded.date_title,
  start_unix = excluded.start_unix,
  end_unix = excluded.end_unix,
  related_canonical_ref = excluded.related_canonical_ref
`)
	if err != nil {
		return err
	}
	defer func() { _ = insWindowStmt.Close() }()

	for _, batch := range batches {
		if batch == nil {
			continue
		}
		for _, upsert := range batch.GetUpserts() {
			if err := applySingleUpsert(ctx, trawler, trawlerDisplayName, upsert, upsertStmt, delAnchorsStmt, insAnchorStmt, insMoneyStmt); err != nil {
				return err
			}
		}
		for _, win := range batch.GetTimeWindows() {
			if win == nil || strings.TrimSpace(win.GetTimeWindowIdentifier()) == "" {
				continue
			}
			if _, err := insWindowStmt.ExecContext(
				ctx,
				win.GetTimeWindowIdentifier(),
				trawler,
				win.GetTimeWindowKind(),
				win.GetPlaceTitle(),
				win.GetDateTitle(),
				win.GetStartUnixSeconds(),
				win.GetEndUnixSeconds(),
				canonicalRefString(win.GetRelatedRecordReference()),
			); err != nil {
				return fmt.Errorf("upsert time window %s: %w", win.GetTimeWindowIdentifier(), err)
			}
		}
	}
	return nil
}

func applySingleUpsert(
	ctx context.Context,
	trawler string,
	trawlerDisplayName string,
	upsert *indexcontract.IndexedArchiveRecordUpsert,
	upsertStmt *sql.Stmt,
	delAnchorsStmt *sql.Stmt,
	insAnchorStmt *sql.Stmt,
	insMoneyStmt *sql.Stmt,
) error {
	stable := canonicalRefString(upsert.GetStableRecordReference())
	if stable == "" {
		return nil
	}
	resolved := canonicalRefString(upsert.GetResolvedRecordReference())
	if resolved == "" {
		resolved = stable
	}
	var occurred sql.NullInt64
	if upsert.GetHasOccurredTime() {
		occurred = sql.NullInt64{Int64: upsert.GetOccurredUnixSeconds(), Valid: true}
	}
	var endUnix sql.NullInt64
	if upsert.GetHasEndTime() {
		endUnix = sql.NullInt64{Int64: upsert.GetEndUnixSeconds(), Valid: true}
	}
	hasTimeOfDay := 0
	if upsert.GetHasTimeOfDay() {
		hasTimeOfDay = 1
	}
	moneyDecimal, moneyCurrency, moneyPending := "", "", 0
	if fin := upsert.GetFinancialMovementFacts(); fin != nil {
		moneyDecimal = fin.GetExactDecimalAmount()
		moneyCurrency = fin.GetIso_4217CurrencyCode()
		if fin.GetMovementIsPending() {
			moneyPending = 1
		}
	}
	peopleText := formatParticipantsForIndex(upsert.GetParticipants())
	title := ExpandEastAsianBigrams(CleanIndexedPlainText(upsert.GetTitle(), 500))
	body := ExpandEastAsianBigrams(CleanIndexedPlainText(upsert.GetBodyText(), 6000))
	meta := TruncateRunes(strings.TrimSpace(strings.Join(nonEmptyStrings(upsert.GetDisplayContextTitle(), upsert.GetMetadataText()), " ")), 1000)
	labelsText := strings.Join(upsert.GetObservedSourceLabels(), " ")
	statusText := strings.Join(upsert.GetStatusLabels(), "\n")

	var docID int64
	if err := upsertStmt.QueryRowContext(
		ctx,
		trawler,
		trawlerDisplayName,
		recordKindString(upsert.GetRecordKind()),
		observedRoleString(upsert.GetObservedSourceRole()),
		stable,
		resolved,
		recordAnchorString(upsert.GetRecordAnchor()),
		strings.TrimSpace(upsert.GetConversationOrThreadIdentifier()),
		strings.TrimSpace(upsert.GetDisplayContextTitle()),
		occurred,
		hasTimeOfDay,
		int(upsert.GetUtcOffsetSeconds()),
		endUnix,
		title,
		peopleText,
		body,
		meta,
		labelsText,
		statusText,
		moneyDecimal,
		moneyCurrency,
		moneyPending,
	).Scan(&docID); err != nil {
		return fmt.Errorf("upsert indexed record %s/%s: %w", trawler, stable, err)
	}

	if _, err := delAnchorsStmt.ExecContext(ctx, docID); err != nil {
		return err
	}
	for _, anchor := range upsert.GetMechanicalJoinAnchors() {
		kind := anchorKindString(anchor.GetAnchorKind())
		val := strings.TrimSpace(anchor.GetAnchorValue())
		if kind == "" || val == "" {
			continue
		}
		if _, err := insAnchorStmt.ExecContext(ctx, docID, kind, val); err != nil {
			return err
		}
	}
	if fin := upsert.GetFinancialMovementFacts(); fin != nil {
		var signedMinor, absMinor, origAbsMinor sql.NullInt64
		if fin.GetHasSignedMinorUnits() {
			signedMinor = sql.NullInt64{Int64: fin.GetSignedMinorUnits(), Valid: true}
		}
		if fin.GetHasAbsoluteMinorUnits() {
			absMinor = sql.NullInt64{Int64: fin.GetAbsoluteMinorUnits(), Valid: true}
		}
		if fin.GetHasOriginalForeignAbsoluteMinorUnits() {
			origAbsMinor = sql.NullInt64{Int64: fin.GetOriginalForeignAbsoluteMinorUnits(), Valid: true}
		}
		if _, err := insMoneyStmt.ExecContext(
			ctx,
			docID,
			trawler,
			stable,
			fin.GetBookingCalendarDate(),
			occurred,
			signedMinor,
			absMinor,
			fin.GetIso_4217CurrencyCode(),
			origAbsMinor,
			fin.GetOriginalForeignIso_4217CurrencyCode(),
			fin.GetCounterpartyDisplayName(),
			fin.GetMovementDescription(),
			fin.GetMerchantCityName(),
			fin.GetMerchantCountryName(),
			moneyPending,
		); err != nil {
			return fmt.Errorf("upsert money movement %s: %w", stable, err)
		}
	}
	return nil
}

func formatParticipantsForIndex(participants []*indexcontract.ArchiveRecordParticipant) string {
	if len(participants) == 0 {
		return ""
	}
	parts := make([]string, 0, len(participants)*2)
	seen := make(map[string]struct{}, len(participants)*2)
	add := func(s string) {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, trimmed)
	}
	for _, p := range participants {
		if p == nil {
			continue
		}
		add(p.GetDisplayName())
		add(p.GetAddressOrHandle())
	}
	return TruncateRunes(strings.Join(parts, " "), 500)
}

func canonicalRefString(ref *identity.CanonicalArchiveRecordReference) string {
	if ref == nil {
		return ""
	}
	return strings.TrimSpace(ref.GetCanonicalArchiveRecordReference())
}

func recordAnchorString(anchor *identity.RecordAnchorIdentifier) string {
	if anchor == nil {
		return ""
	}
	return strings.TrimSpace(anchor.GetRecordAnchorIdentifier())
}

func recordKindString(kind indexcontract.ArchiveRecordKind) string {
	switch kind {
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_EMAIL:
		return "email"
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CHAT_MESSAGE:
		return "message"
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_SOCIAL_POST:
		return "post"
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_NOTE:
		return "note"
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CALENDAR_EVENT:
		return "event"
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_PHOTO:
		return "photo"
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_FINANCIAL_MOVEMENT:
		return "transaction"
	case indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_PERSON:
		return "person"
	default:
		return "record"
	}
}

func observedRoleString(role indexcontract.ObservedArchiveRecordRole) string {
	switch role {
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_AUTHORED:
		return "authored"
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_RECEIVED:
		return "received"
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_BOOKMARK:
		return "bookmark"
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_MENTION:
		return "mention"
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_LIKE:
		return "like"
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_DIRECT_MESSAGE:
		return "dm"
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_REPLY_CONTEXT:
		return "reply_context"
	case indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_OTHER:
		return "other"
	default:
		return ""
	}
}

func anchorKindString(kind indexcontract.MechanicalJoinAnchorKind) string {
	switch kind {
	case indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_RFC5322_MESSAGE_IDENTIFIER:
		return "rfc_msg_id"
	case indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_REGISTRABLE_DOMAIN:
		return "domain"
	case indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_EXACT_MINOR_UNIT_AMOUNT:
		return "amount_minor"
	case indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_BOOKING_OR_ORDER_CODE:
		return "booking_code"
	default:
		return ""
	}
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
