package archive

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSearchIndexRebuildsTrigramCJK(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "calendar.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `
insert into calendars(calendar_id, source_row_id, title)
values ('cal-1', 1, 'Synthetic calendar');
insert into events(
  event_uid, source_row_id, calendar_id, calendar_title, start_time, end_time,
  start_unix, end_unix, summary, description, location_title, participants_text
) values (
  'event-1', 1, 'cal-1', 'Synthetic calendar', '2026-01-01T09:00:00Z', '2026-01-01T10:00:00Z',
  1767258000, 1767261600, 'Synthetic launch', 'meet at 鹤鸣茶社 for launch planning', '鹤鸣茶社', 'Alice Example'
);
delete from schema_migrations;
insert into schema_migrations(version) values (2);
drop table events_fts;
create virtual table events_fts using fts5(event_uid unindexed, summary, description, location, participants);
insert into events_fts(event_uid, summary, description, location, participants)
select event_uid, summary, description, location_title, participants_text from events;
`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	version, err := st.store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}
	assertCalendarSearch(t, st, "茶社")
	assertCalendarSearch(t, st, "鸣茶社")
	assertCalendarSearch(t, st, "鹤鸣茶社")
	assertCalendarSearch(t, st, "launch")
}

func assertCalendarSearch(t *testing.T, st *Store, query string) {
	t.Helper()
	results, total, err := st.Search(context.Background(), query, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(results) != 1 {
		t.Fatalf("query %q returned total=%d rows=%d, want 1", query, total, len(results))
	}
	if query == "茶社" || query == "鸣茶社" {
		t.Logf("query=%q total=%d ref=%s where=%q", query, total, results[0].Ref, results[0].Where)
	}
}
