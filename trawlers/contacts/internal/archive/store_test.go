package archive

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func TestImportContactsSearchWhoAndAnnotate(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	changes, err := st.ImportContacts(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-1",
		Name:       "Ada Example",
		Emails:     []model.ContactValue{{Value: "ada@example.com"}},
		Phones:     []model.ContactValue{{Value: "+15550100"}},
		Accounts:   map[string][]string{"github": {"ada-example"}},
		Tags:       []string{"friend"},
	}}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != "create" {
		t.Fatalf("changes = %#v", changes)
	}
	changes, err = st.ImportContacts(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-1",
		Name:       "Ada Example",
		Emails:     []model.ContactValue{{Value: "ada@example.com"}},
		Phones:     []model.ContactValue{{Value: "+15550100"}},
	}}, false, now)
	if err != nil || len(changes) != 0 {
		t.Fatalf("idempotent changes=%#v err=%v", changes, err)
	}
	results, total, err := st.Search(ctx, "Ada", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(results) != 1 || results[0].Who != "Ada Example" {
		t.Fatalf("search results=%#v total=%d", results, total)
	}
	candidates, err := st.ResolvePeople(ctx, "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Who != "Ada Example" {
		t.Fatalf("candidates = %#v", candidates)
	}
	if _, err := st.SyncContactSnapshot(ctx, "telegram", []model.SourceContact{{
		ExternalID: "telegram-ada", Name: "Ada Telegram", Phones: []model.ContactValue{{Value: "+15550100"}},
	}}, now); err != nil {
		t.Fatal(err)
	}
	candidates, err = st.ResolvePeople(ctx, "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !slices.Contains(candidates[0].Aliases, "Ada Telegram") {
		t.Fatalf("People resolver lost a source-specific alias: %#v", candidates)
	}
	if slices.Contains(candidates[0].Aliases, "friend") {
		t.Fatalf("a search tag escaped into chat identity aliases: %#v", candidates[0].Aliases)
	}
	if _, err := st.AnnotatePerson(ctx, changesPersonID(t, st), "Ada owns billing", "2026-07-09"); err != nil {
		t.Fatal(err)
	}
	annotated, err := st.FindPerson(ctx, "Ada")
	if err != nil {
		t.Fatal(err)
	}
	if annotated.Annotation != "Ada owns billing" || annotated.AnnotationStatedAt != "2026-07-09" {
		t.Fatalf("annotated = %#v", annotated)
	}
}

func TestContactSnapshotReconcilesStaleValuesAndPreservesCorrections(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	first := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	stats, err := st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{
		{ExternalID: "apple-ada", Name: "Ada Example", Emails: []model.ContactValue{{Value: "old@example.com"}}, Phones: []model.ContactValue{{Value: "+15550100"}}},
		{ExternalID: "apple-bea", Name: "Bea Example", Phones: []model.ContactValue{{Value: "+15550200"}}},
	}, first)
	if err != nil || stats.Added != 2 {
		t.Fatalf("first snapshot stats=%#v err=%v", stats, err)
	}
	ada, err := st.FindPerson(ctx, "old@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AnnotatePerson(ctx, ada.ID, "Ada prefers Signal", "2026-07-16"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "telegram", []model.SourceContact{{
		ExternalID: "telegram-ada", Name: "Ada T", Phones: []model.ContactValue{{Value: "+15550100"}}, Accounts: map[string][]string{"telegram": {"ada_example"}},
	}}, first); err != nil {
		t.Fatal(err)
	}
	stats, err = st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-ada", Name: "Ada Example", Emails: []model.ContactValue{{Value: "new@example.com"}},
	}}, first.Add(time.Hour))
	if err != nil || stats.Updated != 1 || stats.Removed != 1 {
		t.Fatalf("replacement stats=%#v err=%v", stats, err)
	}
	ada, err = st.FindPerson(ctx, "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ada.Annotation != "Ada prefers Signal" || len(ada.Phones) != 1 || ada.Phones[0].Source != "telegram" {
		t.Fatalf("preserved person = %#v", ada)
	}
	if _, err := st.FindPerson(ctx, "old@example.com"); err == nil {
		t.Fatal("stale Apple email still resolves")
	}
	if _, err := st.FindPerson(ctx, "Bea Example"); err == nil {
		t.Fatal("person supported only by removed Apple source still exists")
	}
	stats, err = st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-ada", Name: "Ada Example", Emails: []model.ContactValue{{Value: "new@example.com"}},
	}}, first.Add(2*time.Hour))
	if err != nil || stats != (SnapshotStats{}) {
		t.Fatalf("idempotent stats=%#v err=%v", stats, err)
	}
	unchanged, err := st.FindPerson(ctx, "new@example.com")
	if err != nil || !unchanged.UpdatedAt.Equal(ada.UpdatedAt) {
		t.Fatalf("idempotent person updated_at=%v want=%v err=%v", unchanged.UpdatedAt, ada.UpdatedAt, err)
	}
}

func TestSourceContactGroupingLinkCanMoveAndMoveBack(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	if _, err := st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{{ExternalID: "apple-1", Name: "Alex Example", Phones: []model.ContactValue{{Value: "+15550100"}}}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "telegram", []model.SourceContact{{ExternalID: "telegram-1", Name: "Alex Chat", Phones: []model.ContactValue{{Value: "+15550100"}}}}, now); err != nil {
		t.Fatal(err)
	}
	grouped, err := st.FindPerson(ctx, "+15550100")
	if err != nil {
		t.Fatal(err)
	}
	separate := model.NewPerson("Separate Alex", now)
	if err := st.SavePerson(ctx, separate); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveSourceContact(ctx, "telegram", "telegram-1", separate.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "telegram", []model.SourceContact{{ExternalID: "telegram-1", Name: "Alex Chat", Phones: []model.ContactValue{{Value: "+15550100"}}}}, now.Add(90*time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows, err := st.sourceContacts(ctx, "telegram")
	if err != nil || len(rows) != 1 || rows[0].PersonID != separate.ID {
		t.Fatalf("moved source rows=%#v err=%v", rows, err)
	}
	if err := st.MoveSourceContact(ctx, "telegram", "telegram-1", grouped.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "telegram", []model.SourceContact{{ExternalID: "telegram-1", Name: "Alex Chat", Phones: []model.ContactValue{{Value: "+15550100"}}}}, now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err = st.sourceContacts(ctx, "telegram")
	if err != nil || len(rows) != 1 || rows[0].PersonID != grouped.ID {
		t.Fatalf("restored source rows=%#v err=%v", rows, err)
	}
}

func TestExactNameDoesNotMergeContradictoryStrongIdentifiers(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	if _, err := st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{{ExternalID: "apple-1", Name: "Sam Example", Emails: []model.ContactValue{{Value: "one@example.com"}}}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "google", []model.SourceContact{{ExternalID: "google-1", Name: "Sam Example", Emails: []model.ContactValue{{Value: "two@example.com"}}}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "imessage", []model.SourceContact{{ExternalID: "imessage-1", Name: "Sam Example"}}, now); err != nil {
		t.Fatal(err)
	}
	people, err := st.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 {
		t.Fatalf("people = %#v, want two distinct Sams", people)
	}
}

func TestSharedHouseholdIdentifierDoesNotMergeDistinctPeople(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	apple := []model.SourceContact{
		{ExternalID: "apple-georgina", Name: "Georgina Example", Emails: []model.ContactValue{{Value: "georgina@example.com"}}, Phones: []model.ContactValue{{Value: "+15550101"}, {Value: "+15550999"}}},
		{ExternalID: "apple-georgina-duplicate", Name: "Georgina Example", Emails: []model.ContactValue{{Value: "georgina@example.com"}}, Phones: []model.ContactValue{{Value: "+15550101"}, {Value: "+15550999"}}},
		{ExternalID: "apple-michael", Name: "Michael Example", Emails: []model.ContactValue{{Value: "michael@example.com"}}, Phones: []model.ContactValue{{Value: "+15550202"}, {Value: "+15550999"}}},
	}
	stats, err := st.SyncContactSnapshot(ctx, "apple", apple, now)
	if err != nil || stats.Added != 3 {
		t.Fatalf("Apple snapshot stats=%#v err=%v", stats, err)
	}
	georgina, err := st.FindPerson(ctx, "georgina@example.com")
	if err != nil {
		t.Fatal(err)
	}
	michael, err := st.FindPerson(ctx, "michael@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if georgina.ID == michael.ID {
		t.Fatalf("household phone merged distinct people: %#v", georgina)
	}
	if _, err := st.DB().ExecContext(ctx, `update source_contacts set person_id = ? where source = 'apple' and source_id = 'apple-michael'`, georgina.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.rebuildPersonFromSources(ctx, michael.ID, "apple", now); err != nil {
		t.Fatal(err)
	}
	if err := st.rebuildPersonFromSources(ctx, georgina.ID, "apple", now); err != nil {
		t.Fatal(err)
	}
	collapsed, err := st.FindPerson(ctx, "michael@example.com")
	if err != nil || collapsed.ID != georgina.ID {
		t.Fatalf("failed to simulate the historical household merge: person=%#v err=%v", collapsed, err)
	}
	stats, err = st.SyncContactSnapshot(ctx, "apple", apple, now.Add(time.Minute))
	if err != nil || stats != (SnapshotStats{}) {
		t.Fatalf("repair snapshot stats=%#v err=%v", stats, err)
	}
	georgina, err = st.FindPerson(ctx, "georgina@example.com")
	if err != nil {
		t.Fatal(err)
	}
	michael, err = st.FindPerson(ctx, "michael@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if georgina.ID == michael.ID {
		t.Fatalf("replayed snapshot did not repair the historical household merge: %#v", georgina)
	}
	georginaRows, err := st.sourceContactsForPerson(ctx, georgina.ID)
	if err != nil {
		t.Fatal(err)
	}
	georginaAppleRows := 0
	for _, row := range georginaRows {
		if row.Source == "apple" && row.Contact.Name == "Georgina Example" {
			georginaAppleRows++
		}
	}
	if georginaAppleRows != 2 {
		t.Fatalf("household repair split legitimate duplicate cards: rows=%#v", georginaRows)
	}

	messages := []model.SourceContact{
		{Name: "Georgina Example", Phones: []model.ContactValue{{Value: "+15550101"}}},
		{Name: "Michael Example", Phones: []model.ContactValue{{Value: "+15550202"}}},
		{Name: "Example household", Phones: []model.ContactValue{{Value: "+15550999"}}},
	}
	if _, err := st.SyncContactSnapshot(ctx, "imessage", messages, now); err != nil {
		t.Fatal(err)
	}
	georginaAfter, err := st.FindPerson(ctx, "georgina@example.com")
	if err != nil {
		t.Fatal(err)
	}
	michaelAfter, err := st.FindPerson(ctx, "michael@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if georginaAfter.ID != georgina.ID || michaelAfter.ID != michael.ID {
		t.Fatalf("unique mobile identities did not remain stable: Georgina %q -> %q, Michael %q -> %q", georgina.ID, georginaAfter.ID, michael.ID, michaelAfter.ID)
	}
	if len(georginaAfter.Sources["imessage"].Names) == 0 || len(michaelAfter.Sources["imessage"].Names) == 0 {
		t.Fatalf("messaging identities did not join their people: Georgina=%#v Michael=%#v", georginaAfter.Sources, michaelAfter.Sources)
	}

	stats, err = st.SyncContactSnapshot(ctx, "apple", apple, now.Add(time.Hour))
	if err != nil || stats != (SnapshotStats{}) {
		t.Fatalf("repeated Apple snapshot stats=%#v err=%v", stats, err)
	}
	stable, err := st.FindPerson(ctx, "georgina@example.com")
	if err != nil || stable.ID != georgina.ID {
		t.Fatalf("repeated sync changed grouping: person=%#v err=%v", stable, err)
	}

	if err := st.MoveSourceContact(ctx, "apple", "apple-michael", georgina.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "apple", apple, now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	explicitGeorgina, err := st.FindPerson(ctx, "georgina@example.com")
	if err != nil {
		t.Fatal(err)
	}
	explicitMichael, err := st.FindPerson(ctx, "michael@example.com")
	if err != nil || explicitMichael.ID != explicitGeorgina.ID {
		t.Fatalf("replayed snapshot overrode an explicit household grouping: Georgina=%#v Michael=%#v err=%v", explicitGeorgina, explicitMichael, err)
	}
}

func TestImportContactsTreatsSharedHouseholdIdentifierAsAmbiguous(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	contacts := []model.SourceContact{
		{Name: "Ada Example", Emails: []model.ContactValue{{Value: "ada@example.com"}}, Phones: []model.ContactValue{{Value: "+15550100"}, {Value: "+15550999"}}},
		{Name: "Bob Example", Emails: []model.ContactValue{{Value: "bob@example.com"}}, Phones: []model.ContactValue{{Value: "+15550200"}, {Value: "+15550999"}}},
	}
	changes, err := st.ImportContacts(ctx, "apple", contacts, false, time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC))
	if err != nil || len(changes) != 2 || changes[0].Action != "create" || changes[1].Action != "create" || changes[0].PersonID == changes[1].PersonID {
		t.Fatalf("household import changes=%#v err=%v", changes, err)
	}
}

func TestSearchKeepsDistinctContactNoteAndSourceNameMatches(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	person := model.Person{ID: "person-example", Name: "Avery Example", Sources: map[string]model.PersonSource{"fixture": {Names: []string{"Lantern alias"}}}}
	if err := st.SavePerson(ctx, person); err != nil {
		t.Fatal(err)
	}
	for index, body := range []string{"First needle note", "Second needle note"} {
		if err := st.SaveNote(ctx, model.Note{ID: fmt.Sprintf("note-%d", index+1), PersonID: person.ID, Body: body, OccurredAt: time.Date(2026, 7, 9+index, 10, 0, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	notes, total, err := st.Search(ctx, "needle", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(notes) != 2 || notes[0].Ref != notes[1].Ref || notes[0].AnchorID == notes[1].AnchorID {
		t.Fatalf("note matches = %#v, total=%d", notes, total)
	}
	alias, total, err := st.Search(ctx, "Lantern alias", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(alias) != 1 || alias[0].AnchorID != "source_name" || len(alias[0].Matches) != 1 || alias[0].Matches[0].Field != "source_name" {
		t.Fatalf("source-name match = %#v, total=%d", alias, total)
	}
}

func TestImportContactsRetainsAvatar(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	avatarData := pngBytes()
	if _, err := st.ImportContacts(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-1",
		Name:       "Ada Avatar",
		Avatar:     &model.SourceAvatar{Data: avatarData},
	}}, false, now); err != nil {
		t.Fatal(err)
	}
	person, err := st.FindPerson(ctx, "Ada Avatar")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(person.Avatar.Data, avatarData) || person.Avatar.MIME != "image/png" || person.Avatar.SHA256 == "" || person.Avatar.Source != "apple" {
		t.Fatalf("avatar = %#v", person.Avatar)
	}
}

func TestLegacyImportPreservesPeopleNotesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	legacy := filepath.Join(t.TempDir(), "share")
	writeLegacyPerson(t, legacy, "legacy-ada-folder", `---
id: person_ada
name: Ada Legacy
tags: [vip]
avatar:
  path: avatar.png
  source: legacy
  mime: image/png
  updated_at: 2026-07-02T10:00:00Z
emails:
  - value: ada@example.com
phones:
  - value: "+15550100"
accounts:
  telegram: [ada_legacy]
sources:
  telegram:
    names: [Ada Telegram]
    phones: ["+15550100"]
    last_seen_at: 2026-07-08T10:00:00Z
created_at: 2026-07-01T10:00:00Z
updated_at: 2026-07-02T10:00:00Z
---
# Ada Legacy

Legacy body.
`)
	writeLegacyAvatar(t, legacy, "legacy-ada-folder", pngBytes())
	writeLegacyNote(t, legacy, "legacy-ada-folder", `---
id: note_ada
person_id: person_ada
occurred_at: 2026-07-08T09:00:00Z
captured_at: 2026-07-08T10:00:00Z
kind: dm
source: telegram
topics: [handoff]
privacy: normal
---
Discuss the handoff.
`)
	summary, err := st.ImportLegacy(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if summary.People != 1 || summary.Notes != 1 || summary.Created != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	summary, err = st.ImportLegacy(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if summary.People != 1 || summary.Notes != 1 || summary.Unchanged != 1 {
		t.Fatalf("rerun summary = %#v", summary)
	}
	person, err := st.FindPerson(ctx, "telegram:ada_legacy")
	if err != nil {
		t.Fatal(err)
	}
	if person.Name != "Ada Legacy" || person.Body == "" || person.Sources["telegram"].Names[0] != "Ada Telegram" {
		t.Fatalf("person = %#v", person)
	}
	if _, err := st.FindPerson(ctx, "legacy-ada-folder"); err != nil {
		t.Fatalf("legacy folder slug did not resolve: %v", err)
	}
	candidates, err := st.ResolvePeople(ctx, "legacy-ada-folder")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Who != "Ada Legacy" {
		t.Fatalf("folder slug candidates = %#v", candidates)
	}
	if !bytes.Equal(person.Avatar.Data, pngBytes()) {
		t.Fatalf("avatar data = %x, want %x", person.Avatar.Data, pngBytes())
	}
	results, total, err := st.Search(ctx, "handoff", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || results[0].Ref != PersonRef("person_ada") || results[0].ShortRef == "" {
		t.Fatalf("note search results=%#v total=%d", results, total)
	}
}

func TestLegacyImportDerivesStableIDsAndDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	legacy := filepath.Join(t.TempDir(), "share")
	writeLegacyPerson(t, legacy, "idless-folder", `---
name: Stable Legacy
emails:
  - value: stable@example.com
created_at: 2026-07-01T10:00:00Z
updated_at: 2026-07-02T10:00:00Z
---
# Stable Legacy
`)
	writeLegacyNote(t, legacy, "idless-folder", `---
occurred_at: 2026-07-08T09:00:00Z
captured_at: 2026-07-08T10:00:00Z
kind: note
source: manual
privacy: normal
---
Stable note.
`)
	first, err := st.ImportLegacy(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if first.People != 1 || first.Notes != 1 || first.Created != 1 || first.DerivedIDs != 2 {
		t.Fatalf("first summary = %#v", first)
	}
	person, err := st.FindPerson(ctx, "idless-folder")
	if err != nil {
		t.Fatal(err)
	}
	notes, err := st.Notes(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %#v", notes)
	}
	second, err := st.ImportLegacy(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if second.People != 1 || second.Notes != 1 || second.Unchanged != 1 || second.DerivedIDs != 2 {
		t.Fatalf("second summary = %#v", second)
	}
	people, err := st.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 {
		t.Fatalf("people = %#v", people)
	}
	notes, err = st.Notes(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("rerun notes = %#v", notes)
	}
}

func TestShortRefRecords(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	if err := st.SavePerson(ctx, model.Person{ID: "person_ada", Name: "Ada Example", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	records, err := st.ShortRefRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Ref != PersonRef("person_ada") {
		t.Fatalf("records = %#v", records)
	}
}

func openTempStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "contacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func changesPersonID(t *testing.T, st *Store) string {
	t.Helper()
	person, err := st.FindPerson(context.Background(), "Ada")
	if err != nil {
		t.Fatal(err)
	}
	return person.ID
}

func writeLegacyPerson(t *testing.T, root, slug, data string) {
	t.Helper()
	path := filepath.Join(root, "people", slug, "person.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyNote(t *testing.T, root, slug, data string) {
	t.Helper()
	path := filepath.Join(root, "people", slug, "notes", "note.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyAvatar(t *testing.T, root, slug string, data []byte) {
	t.Helper()
	path := filepath.Join(root, "people", slug, "avatar.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func pngBytes() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
}
