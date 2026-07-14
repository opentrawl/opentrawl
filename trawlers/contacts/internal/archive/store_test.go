package archive

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/vcard"
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

func TestImportContactsAvatarRoundTripsToVCard(t *testing.T) {
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
	assertVCardAvatar(t, person, avatarData)
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
	assertVCardAvatar(t, person, pngBytes())
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

func assertVCardAvatar(t *testing.T, person model.Person, data []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := vcard.WriteWithOptions(&buf, []model.Person{person}, vcard.Options{IncludeAvatars: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "PHOTO:data:image/png;base64,") {
		t.Fatalf("missing avatar photo: %s", out)
	}
	if !strings.Contains(out, "iVBORw0KGgo=") {
		t.Fatalf("avatar bytes were not exported: %s", out)
	}
	if !bytes.Equal(person.Avatar.Data, data) {
		t.Fatalf("avatar data = %x, want %x", person.Avatar.Data, data)
	}
}

func pngBytes() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
}
