package archive

import (
	"context"
	"database/sql"

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
	var updated string
	_ = db.QueryRowContext(ctx, `select coalesce(max(updated_at), '') from people`).Scan(&updated)
	out.UpdatedAt = parseTime(updated)
	return out, nil
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
