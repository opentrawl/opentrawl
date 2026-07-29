package archive

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/state"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{ArchivePath: s.path, ArchiveBytes: fileSize(s.path)}
	version, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	out.SchemaVersion = version
	db := s.store.DB()
	if out.People, err = countTable(ctx, db, "people"); err != nil {
		return Status{}, err
	}
	if out.Notes, err = countTable(ctx, db, "notes"); err != nil {
		return Status{}, err
	}
	if out.Sources, err = countSources(ctx, db); err != nil {
		return Status{}, err
	}
	out.LastSuccessfullyCompletedArchiveSyncTime, err = lastSuccessfullyCompletedArchiveSyncTime(ctx, db)
	if err != nil {
		return Status{}, err
	}
	return out, nil
}

func lastSuccessfullyCompletedArchiveSyncTime(ctx context.Context, db *sql.DB) (time.Time, error) {
	var markerTableExists bool
	if err := db.QueryRowContext(ctx, `select exists(select 1 from sqlite_master where type = 'table' and name = 'sync_state')`).Scan(&markerTableExists); err != nil {
		return time.Time{}, err
	}
	if !markerTableExists {
		return time.Time{}, nil
	}
	record, found, err := state.New(db).Get(
		ctx,
		AppID,
		contactsArchiveSyncMarkerEntityType,
		lastSuccessfullyCompletedContactsArchiveSyncTimeMarkerIdentity,
	)
	if err != nil || !found {
		return time.Time{}, err
	}
	completedAt, err := time.Parse(time.RFC3339Nano, record.Value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse last successfully completed Contacts archive sync time: %w", err)
	}
	return completedAt, nil
}

func countTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, `select count(*) from `+ckstore.QuoteIdent(table)).Scan(&count)
	return count, err
}

func countSources(ctx context.Context, db *sql.DB) (int64, error) {
	rows, err := db.QueryContext(ctx, `
select source from contact_values where trim(source) <> ''
union
select json_each.key from people, json_each(people.sources_json)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var count int64
	for rows.Next() {
		count++
	}
	return count, rows.Err()
}
