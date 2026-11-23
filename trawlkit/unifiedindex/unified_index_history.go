package unifiedindex

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func backfillMissingTrawlerMonthlyHistories(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
select s.trawler
from indexed_trawler_state s
where s.indexed_at_utc <> ''
  and not exists (
    select 1 from indexed_trawler_history_summary h where h.trawler = s.trawler
  )
order by s.trawler asc
`)
	if err != nil {
		return fmt.Errorf("list trawlers missing monthly history: %w", err)
	}
	var missing []string
	for rows.Next() {
		var trawler string
		if scanErr := rows.Scan(&trawler); scanErr != nil {
			_ = rows.Close()
			return fmt.Errorf("scan trawler missing monthly history: %w", scanErr)
		}
		if trimmed := strings.TrimSpace(trawler); trimmed != "" {
			missing = append(missing, trimmed)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	if len(missing) == 0 {
		return nil
	}

	indexWriteMutex.Lock()
	defer indexWriteMutex.Unlock()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin monthly history backfill transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, trawler := range missing {
		if err := refreshTrawlerMonthlyHistoryInsideTx(ctx, tx, trawler); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func refreshTrawlerMonthlyHistoryInsideTx(ctx context.Context, tx *sql.Tx, trawler string) error {
	if _, err := tx.ExecContext(ctx, `delete from indexed_trawler_monthly_counts where trawler = ?`, trawler); err != nil {
		return fmt.Errorf("clear monthly counts for %s: %w", trawler, err)
	}
	if _, err := tx.ExecContext(ctx, `delete from indexed_trawler_history_summary where trawler = ?`, trawler); err != nil {
		return fmt.Errorf("clear history summary for %s: %w", trawler, err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into indexed_trawler_monthly_counts(trawler, calendar_year, calendar_month, record_count)
select
  trawler,
  cast(strftime('%Y', occurred_unix, 'unixepoch') as integer) as calendar_year,
  cast(strftime('%m', occurred_unix, 'unixepoch') as integer) as calendar_month,
  count(*)
from indexed_archive_records
where trawler = ?
  and occurred_unix is not null
  and occurred_unix <> 0
  and strftime('%Y', occurred_unix, 'unixepoch') is not null
group by calendar_year, calendar_month
having calendar_year > 0 and calendar_month between 1 and 12
`, trawler); err != nil {
		return fmt.Errorf("rebuild monthly counts for %s: %w", trawler, err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into indexed_trawler_history_summary(trawler, oldest_occurred_unix, undated_record_count)
select
  ?,
  min(case
    when occurred_unix is not null
     and occurred_unix <> 0
     and strftime('%Y', occurred_unix, 'unixepoch') is not null
     and cast(strftime('%Y', occurred_unix, 'unixepoch') as integer) > 0
    then occurred_unix
  end),
  coalesce(sum(case
    when occurred_unix is null
      or occurred_unix = 0
      or strftime('%Y', occurred_unix, 'unixepoch') is null
      or cast(strftime('%Y', occurred_unix, 'unixepoch') as integer) <= 0
    then 1 else 0
  end), 0)
from indexed_archive_records
where trawler = ?
`, trawler, trawler); err != nil {
		return fmt.Errorf("rebuild history summary for %s: %w", trawler, err)
	}
	return nil
}

// ReadAllTrawlerMonthlyHistories reads the precomputed per-month record counts
// and undated totals from index.db without writing to the database.
// If the read-only database predates the monthly history tables, it returns
// (nil, nil) so callers treat those archives as unmeasured until the next update.
func ReadAllTrawlerMonthlyHistories(
	ctx context.Context,
	db *sql.DB,
) (map[string]*status.ArchiveMonthlyHistory, error) {
	if db == nil {
		return nil, nil
	}
	var tableCount int
	if err := db.QueryRowContext(ctx, `
select count(*)
from sqlite_master
where type = 'table'
  and name in ('indexed_trawler_monthly_counts', 'indexed_trawler_history_summary')
`).Scan(&tableCount); err != nil {
		return nil, fmt.Errorf("check monthly history tables: %w", err)
	}
	if tableCount < 2 {
		return nil, nil
	}

	histories := make(map[string]*status.ArchiveMonthlyHistory, 16)

	summaryRows, err := db.QueryContext(ctx, `
select trawler, oldest_occurred_unix, undated_record_count
from indexed_trawler_history_summary
order by trawler asc
`)
	if err != nil {
		return nil, fmt.Errorf("read trawler history summaries: %w", err)
	}
	for summaryRows.Next() {
		var trawler string
		var oldestUnix sql.NullInt64
		var undatedCount int64
		if scanErr := summaryRows.Scan(&trawler, &oldestUnix, &undatedCount); scanErr != nil {
			_ = summaryRows.Close()
			return nil, fmt.Errorf("scan trawler history summary: %w", scanErr)
		}
		history := &status.ArchiveMonthlyHistory{}
		if oldestUnix.Valid {
			history.OldestArchivedRecordTime = timestamppb.New(time.Unix(oldestUnix.Int64, 0).UTC())
		}
		if undatedCount > 0 {
			history.UndatedRecordCount = uint64(undatedCount)
		}
		histories[trawler] = history
	}
	if err := summaryRows.Err(); err != nil {
		_ = summaryRows.Close()
		return nil, err
	}
	_ = summaryRows.Close()

	countRows, err := db.QueryContext(ctx, `
select trawler, calendar_year, calendar_month, record_count
from indexed_trawler_monthly_counts
order by trawler asc, calendar_year asc, calendar_month asc
`)
	if err != nil {
		return nil, fmt.Errorf("read trawler monthly counts: %w", err)
	}
	defer func() { _ = countRows.Close() }()

	for countRows.Next() {
		var trawler string
		var year, month int64
		var count int64
		if scanErr := countRows.Scan(&trawler, &year, &month, &count); scanErr != nil {
			return nil, fmt.Errorf("scan trawler monthly count: %w", scanErr)
		}
		if year <= 0 || month < 1 || month > 12 || count <= 0 {
			continue
		}
		history, ok := histories[trawler]
		if !ok {
			history = &status.ArchiveMonthlyHistory{}
			histories[trawler] = history
		}
		history.MonthlyRecordCountsInChronologicalOrder = append(
			history.MonthlyRecordCountsInChronologicalOrder,
			&status.ArchiveMonthlyRecordCount{
				CalendarYear:  uint32(year),
				CalendarMonth: uint32(month),
				RecordCount:   uint64(count),
			},
		)
	}
	if err := countRows.Err(); err != nil {
		return nil, err
	}
	return histories, nil
}
