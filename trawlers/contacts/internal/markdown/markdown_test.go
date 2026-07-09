package markdown

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPersonFrontmatter(t *testing.T) {
	path := writeFixture(t, "people/ada/person.md", `---
id: person_ada
name: Ada Example
aka: Ada E.
tags: [work]
emails:
  - value: ada@example.com
    label: work
    source: manual
    primary: true
phones:
  - value: "+15550100"
accounts:
  github: [ada]
annotation: "Ada owns billing"
annotation_stated_at: "2026-07-09"
created_at: 2026-07-09T10:00:00Z
updated_at: 2026-07-09T10:00:00Z
---
# Ada Example

Body text.
`)
	person, report, err := ReadPerson(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Needed {
		t.Fatalf("unexpected repair report: %#v", report)
	}
	if person.ID != "person_ada" || person.Name != "Ada Example" || person.Emails[0].Value != "ada@example.com" || person.Accounts["github"][0] != "ada" {
		t.Fatalf("person = %#v", person)
	}
	if person.Annotation != "Ada owns billing" || strings.TrimSpace(person.Body) != "# Ada Example\n\nBody text." {
		t.Fatalf("annotation/body = %#v body=%q", person.Annotation, person.Body)
	}
	if person.AnnotationStatedAt != "2026-07-09" {
		t.Fatalf("annotation stated at = %q", person.AnnotationStatedAt)
	}
}

func TestReadPersonMissingFrontmatterInfersNameFromHeading(t *testing.T) {
	path := writeFixture(t, "people/ada/person.md", "# Ada Heading\n\nBody")
	person, report, err := ReadPerson(path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Needed || person.Name != "Ada Heading" || person.ID == "" || report.DerivedIDs != 1 {
		t.Fatalf("person=%#v report=%#v", person, report)
	}
	again, _, err := ReadPerson(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != person.ID {
		t.Fatalf("stable id changed: %q then %q", person.ID, again.ID)
	}
}

func TestReadPersonSalvagesBrokenFrontmatter(t *testing.T) {
	path := writeFixture(t, "people/ada/person.md", "---\nid: person_1\nname: Ada Example\ntags: [one\n---\n# Ada\n")
	person, report, err := ReadPerson(path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Needed || person.ID != "person_1" || person.Name != "Ada Example" {
		t.Fatalf("person=%#v report=%#v", person, report)
	}
}

func TestReadNoteFrontmatter(t *testing.T) {
	path := writeFixture(t, "people/ada/notes/note.md", `---
id: note_1
person_id: person_ada
occurred_at: 2026-07-09T09:00:00Z
captured_at: 2026-07-09T10:00:00Z
kind: dm
source: manual
topics: [billing]
privacy: normal
---
Follow up.
`)
	note, report, err := ReadNote(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Needed || note.ID != "note_1" || note.PersonID != "person_ada" || note.Topics[0] != "billing" {
		t.Fatalf("note=%#v report=%#v", note, report)
	}
}

func TestReadNoteMissingIDDerivesStableID(t *testing.T) {
	path := writeFixture(t, "people/ada/notes/note.md", `---
person_id: person_ada
occurred_at: 2026-07-09T09:00:00Z
captured_at: 2026-07-09T10:00:00Z
kind: dm
source: manual
privacy: normal
---
Follow up.
`)
	note, report, err := ReadNoteWithStableID(path, "people/ada/notes/note.md")
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := ReadNoteWithStableID(path, "people/ada/notes/note.md")
	if err != nil {
		t.Fatal(err)
	}
	if report.DerivedIDs != 1 || note.ID == "" || again.ID != note.ID {
		t.Fatalf("note=%#v again=%#v report=%#v", note, again, report)
	}
}

func writeFixture(t *testing.T, rel, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
