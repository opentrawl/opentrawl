package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/markdown"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func (s *Store) ImportLegacy(ctx context.Context, path string) (ImportSummary, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ImportSummary{}, errors.New("legacy contacts path is required")
	}
	people, err := readLegacyPeople(path)
	if err != nil {
		return ImportSummary{}, err
	}
	var summary ImportSummary
	for _, item := range people {
		action, err := s.UpsertPerson(ctx, item.Person)
		if err != nil {
			return ImportSummary{}, err
		}
		summary.People++
		switch action {
		case "created":
			summary.Created++
		case "updated":
			summary.Updated++
		default:
			summary.Unchanged++
		}
		summary.DerivedIDs += item.DerivedIDs
		for _, note := range item.Notes {
			if _, err := s.UpsertNote(ctx, note); err != nil {
				return ImportSummary{}, err
			}
			summary.Notes++
		}
	}
	return summary, nil
}

func readLegacyPeople(root string) ([]personWithNotes, error) {
	peopleDir := filepath.Join(root, "people")
	entries, err := os.ReadDir(peopleDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := make([]personWithNotes, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		personPath := filepath.Join(peopleDir, entry.Name(), "person.md")
		if _, err := os.Stat(personPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		personRel := filepath.ToSlash(filepath.Join("people", entry.Name(), "person.md"))
		person, report, err := markdown.ReadPersonWithStableID(personPath, personRel)
		if err != nil {
			return nil, err
		}
		person.AKA = appendMissingStrings(person.AKA, []string{entry.Name()})
		person.Path = ""
		notes, derivedNoteIDs, err := readLegacyNotes(filepath.Join(peopleDir, entry.Name(), "notes"), filepath.ToSlash(filepath.Join("people", entry.Name(), "notes")), person.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, personWithNotes{Person: person, Notes: notes, DerivedIDs: report.DerivedIDs + derivedNoteIDs})
	}
	return out, nil
}

func readLegacyNotes(dir, stableDir, personID string) ([]model.Note, int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	notes := make([]model.Note, 0, len(entries))
	derivedIDs := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		note, report, err := markdown.ReadNoteWithStableID(filepath.Join(dir, entry.Name()), filepath.ToSlash(filepath.Join(stableDir, entry.Name())))
		if err != nil {
			return nil, 0, err
		}
		derivedIDs += report.DerivedIDs
		if note.PersonID == "" {
			note.PersonID = personID
		}
		note.Path = ""
		notes = append(notes, note)
	}
	return notes, derivedIDs, nil
}
