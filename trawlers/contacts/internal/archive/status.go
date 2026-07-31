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
	db := s.store.DB()
	archivedPersonCount, err := countTable(ctx, db, "people")
	if err != nil {
		return Status{}, err
	}
	out.People = archivedPersonCount
	archivedContactNoteCount, err := countTable(ctx, db, "notes")
	if err != nil {
		return Status{}, err
	}
	out.Notes = archivedContactNoteCount
	contactSourceCount, err := countSources(ctx, db)
	if err != nil {
		return Status{}, err
	}
	out.Sources = contactSourceCount
	out.LastSuccessfullyCompletedArchiveSyncTime, err = lastSuccessfullyCompletedArchiveSyncTime(ctx, db)
	if err != nil {
		return Status{}, err
	}
	return out, nil
}

func lastSuccessfullyCompletedArchiveSyncTime(ctx context.Context, db *sql.DB) (time.Time, error) {
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
