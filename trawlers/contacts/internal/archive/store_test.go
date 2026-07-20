package archive

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/avatar"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func TestContactSnapshotSearchWhoAndAnnotate(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	_, err := st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-1",
		Name:       "Ada Example",
		Emails:     []model.ContactValue{{Value: "ada@example.com"}},
		Phones:     []model.ContactValue{{Value: "+15550100"}},
		Accounts:   map[string][]string{"github": {"ada-example"}},
		Tags:       []string{"friend"},
	}}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-1",
		Name:       "Ada Example",
		Emails:     []model.ContactValue{{Value: "ada@example.com"}},
		Phones:     []model.ContactValue{{Value: "+15550100"}},
	}}, now)
	if err != nil {
		t.Fatalf("idempotent snapshot: %v", err)
	}
	results, total, err := st.Search(ctx, "Ada", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(results) != 1 || results[0].Who != "Ada Example" {
		t.Fatalf("search results=%#v total=%d", results, total)
	}
	results, total, err = st.Search(ctx, "Ada", SearchOptions{After: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(results) != 0 {
		t.Fatalf("date-bounded search returned timeless contact results=%#v total=%d", results, total)
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

func TestContactSnapshotRollsBackWholeReplacementOnWriteFailure(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	first := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	initial := []model.SourceContact{
		{ExternalID: "apple-ada", Name: "Ada Example", Emails: []model.ContactValue{{Value: "old@example.com"}}},
		{ExternalID: "apple-bea", Name: "Bea Example", Phones: []model.ContactValue{{Value: "+15550200"}}},
	}
	if _, err := st.SyncContactSnapshot(ctx, "apple", initial, first); err != nil {
		t.Fatal(err)
	}
	peopleBefore, err := st.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contactsBefore, err := st.sourceContacts(ctx, "apple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
create trigger fail_contact_snapshot
before insert on source_contacts
when new.source_id = 'apple-fail'
begin
  select raise(abort, 'injected contact snapshot failure');
end`); err != nil {
		t.Fatal(err)
	}

	replacement := []model.SourceContact{
		{ExternalID: "apple-ada", Name: "Ada Example", Emails: []model.ContactValue{{Value: "new@example.com"}}},
		{ExternalID: "apple-cora", Name: "Cora Example", Phones: []model.ContactValue{{Value: "+15550300"}}},
		{ExternalID: "apple-fail", Name: "Failure Example", Phones: []model.ContactValue{{Value: "+15550400"}}},
	}
	if _, err := st.SyncContactSnapshot(ctx, "apple", replacement, first.Add(time.Hour)); err == nil {
		t.Fatal("replacement succeeded despite injected database failure")
	}
	peopleAfter, err := st.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contactsAfter, err := st.sourceContacts(ctx, "apple")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(peopleAfter, peopleBefore) || !reflect.DeepEqual(contactsAfter, contactsBefore) {
		t.Fatalf("failed replacement changed archive: people before=%#v after=%#v; contacts before=%#v after=%#v", peopleBefore, peopleAfter, contactsBefore, contactsAfter)
	}

	if _, err := st.DB().ExecContext(ctx, `drop trigger fail_contact_snapshot`); err != nil {
		t.Fatal(err)
	}
	stats, err := st.SyncContactSnapshot(ctx, "apple", replacement, first.Add(time.Hour))
	if err != nil || stats != (SnapshotStats{Added: 2, Updated: 1, Removed: 1}) {
		t.Fatalf("retry stats=%#v err=%v", stats, err)
	}
	if _, err := st.FindPerson(ctx, "new@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FindPerson(ctx, "Bea Example"); err == nil {
		t.Fatal("successful replacement retained removed contact")
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

func TestContactSnapshotTreatsSharedHouseholdIdentifierAsAmbiguous(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	contacts := []model.SourceContact{
		{Name: "Ada Example", Emails: []model.ContactValue{{Value: "ada@example.com"}}, Phones: []model.ContactValue{{Value: "+15550100"}, {Value: "+15550999"}}},
		{Name: "Bob Example", Emails: []model.ContactValue{{Value: "bob@example.com"}}, Phones: []model.ContactValue{{Value: "+15550200"}, {Value: "+15550999"}}},
	}
	if _, err := st.SyncContactSnapshot(ctx, "apple", contacts, time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	people, err := st.People(ctx)
	if err != nil || len(people) != 2 || people[0].ID == people[1].ID {
		t.Fatalf("household snapshot people=%#v err=%v", people, err)
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

func TestContactSnapshotRetainsAvatar(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	avatarData := pngBytes()
	importedAvatar, err := avatar.InspectBytes(avatarData)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{{
		ExternalID: "apple-1",
		Name:       "Ada Avatar",
		Avatar:     &importedAvatar,
	}}, now); err != nil {
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

func pngBytes() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
}
