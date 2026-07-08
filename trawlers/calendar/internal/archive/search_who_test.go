package archive

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TRAWL-122 round-2 review: the who-count/search-count fix must filter on an
// entity's raw display spellings, not its cleaned label. "Bea <bea@example.com>"
// cleans to the label "Bea"; a different person also displayed as "Bea" on a
// different email is a separate entity. Matching the label would pull that
// other entity's events in — a cross-entity leak. Searching one entity must
// return only its own event, and its search count must equal its who count.
func TestSearchWhoFilterUsesRawNamesNotCleanedLabel(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)

	if _, err := st.DB().Exec(`insert into calendars(calendar_id, source_row_id, title) values ('cal', 0, 'Work')`); err != nil {
		t.Fatal(err)
	}
	insertWhoEvent(t, st.DB(), "event-bea-cruft", "Bea <bea@example.com>", "bea@example.com", 1000)
	insertWhoEvent(t, st.DB(), "event-bea-plain", "Bea", "other-bea@example.com", 2000)

	candidates, err := st.WhoCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var owner *WhoCandidate
	for i := range candidates {
		if hasStringValue(candidates[i].filterIdentifiers, "bea@example.com") {
			owner = &candidates[i]
			break
		}
	}
	if owner == nil {
		t.Fatalf("candidates = %#v, want one owning bea@example.com", candidates)
	}
	if owner.Who != "Bea" || owner.Messages != 1 {
		t.Fatalf("owner candidate = %#v, want cleaned label Bea with 1 event", *owner)
	}

	results, total, err := st.Search(ctx, "", SearchOptions{Who: owner.Filter()})
	if err != nil {
		t.Fatal(err)
	}
	if total != owner.Messages || len(results) != 1 {
		t.Fatalf("search by owner = %d results, total %d, want its own single event (who count %d)", len(results), total, owner.Messages)
	}
	if results[0].Ref != RefForUID("event-bea-cruft") {
		t.Fatalf("search returned %q, leaked the other Bea's event", results[0].Ref)
	}
}

func openTempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "calcrawl.db")
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func insertWhoEvent(t *testing.T, db *sql.DB, uid, organizerName, organizerEmail string, startUnix int64) {
	t.Helper()
	_, err := db.Exec(`insert into events(
		event_uid, source_row_id, calendar_id, calendar_title, start_time, end_time,
		start_unix, end_unix, summary, organizer_name, organizer_email
	) values (?, 0, 'cal', 'Work', '2026-04-15T10:00:00Z', '2026-04-15T11:00:00Z', ?, ?, ?, ?, ?)`,
		uid, startUnix, startUnix+3600, "Event "+uid, organizerName, organizerEmail)
	if err != nil {
		t.Fatalf("insert event %q: %v", uid, err)
	}
	if _, err := db.Exec(`insert into participants(event_uid, position, display_name, email) values (?, 0, ?, ?)`,
		uid, organizerName, organizerEmail); err != nil {
		t.Fatalf("insert participant %q: %v", uid, err)
	}
}

func hasStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
