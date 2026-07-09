package archive

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func (s *Store) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, int, error) {
	query = strings.ToLower(strings.Join(strings.Fields(query), " "))
	if query == "" {
		return []SearchResult{}, 0, nil
	}
	people, err := s.People(ctx)
	if err != nil {
		return nil, 0, err
	}
	byID := map[string]model.Person{}
	for _, person := range people {
		byID[person.ID] = person
	}
	hits := []scoredHit{}
	seenPeople := map[string]bool{}
	indexHits, err := s.searchPeopleFTS(ctx, query, byID)
	if err != nil {
		return nil, 0, err
	}
	for _, hit := range indexHits {
		seenPeople[hit.PersonID] = true
		hits = append(hits, hit)
	}
	for _, person := range people {
		text := personSearchText(person)
		if score := scoreText(text, query); score > 0 && !seenPeople[person.ID] {
			hits = append(hits, scoredHit{PersonID: person.ID, Who: person.Name, Score: score, Snippet: personSnippet(person, query)})
		}
		notes, err := s.Notes(ctx, person.ID)
		if err != nil {
			return nil, 0, err
		}
		for _, note := range notes {
			text := strings.ToLower(strings.Join(append([]string{note.Kind, note.Source, note.Body}, note.Topics...), " "))
			if score := scoreText(text, query); score > 0 {
				hits = append(hits, scoredHit{PersonID: person.ID, Who: person.Name, Score: score, Snippet: snippet(note.Body, query), Time: note.OccurredAt})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].PersonID < hits[j].PersonID
		}
		return hits[i].Score > hits[j].Score
	})
	aliases, err := s.currentShortRefs(ctx)
	if err != nil {
		return nil, 0, err
	}
	results := make([]SearchResult, 0, len(hits))
	for _, hit := range hits {
		if !withinRange(hit.Time, options.After, options.Before) {
			continue
		}
		ref := PersonRef(hit.PersonID)
		results = append(results, SearchResult{
			Ref:      ref,
			Time:     hit.Time,
			Who:      hit.Who,
			Snippet:  hit.Snippet,
			PersonID: hit.PersonID,
			ShortRef: aliases[ref],
		})
	}
	total := len(results)
	if options.Limit > 0 && len(results) > options.Limit {
		results = results[:options.Limit]
	}
	return results, total, nil
}

func (s *Store) currentShortRefs(ctx context.Context) (map[string]string, error) {
	records, err := s.ShortRefRecords(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(records))
	for _, record := range records {
		refs = append(refs, record.Ref)
	}
	entries, err := shortref.BuildSlice(refs)
	if err != nil {
		return nil, err
	}
	aliases := make(map[string]string, len(entries))
	for _, entry := range entries {
		aliases[entry.FullRef] = entry.Alias
	}
	return aliases, nil
}

type scoredHit struct {
	PersonID string
	Who      string
	Score    int
	Snippet  string
	Time     time.Time
}

func (s *Store) searchPeopleFTS(ctx context.Context, query string, people map[string]model.Person) ([]scoredHit, error) {
	match := ftsPrefixQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select person_id
from people_fts
where people_fts match ?
order by bm25(people_fts), person_id`, match)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	hits := []scoredHit{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		person, ok := people[id]
		if !ok {
			continue
		}
		hits = append(hits, scoredHit{PersonID: id, Who: person.Name, Score: 100, Snippet: personSnippet(person, query)})
	}
	return hits, rows.Err()
}

func withinRange(t, after, before time.Time) bool {
	if t.IsZero() {
		return true
	}
	if !after.IsZero() && t.Before(after) {
		return false
	}
	return before.IsZero() || t.Before(before)
}

func personSearchText(person model.Person) string {
	parts := []string{person.ID, person.Name, person.SortName, person.Body, person.Annotation}
	parts = append(parts, person.AKA...)
	parts = append(parts, person.Tags...)
	for _, source := range person.Sources {
		parts = append(parts, source.Names...)
	}
	for _, email := range person.Emails {
		parts = append(parts, email.Value)
	}
	for _, phone := range person.Phones {
		parts = append(parts, phone.Value)
	}
	for _, address := range person.Addresses {
		parts = append(parts, address.Value)
	}
	for service, values := range person.Accounts {
		parts = append(parts, service)
		parts = append(parts, values...)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func personSnippet(person model.Person, query string) string {
	text := personDisplayText(person)
	if s := snippet(text, query); s != "" {
		return s
	}
	if text != "" {
		return ckstore.FTS5Snippet(text, query)
	}
	return person.Name
}

func personDisplayText(person model.Person) string {
	parts := []string{}
	parts = append(parts, person.Tags...)
	for _, email := range person.Emails {
		parts = append(parts, email.Value)
	}
	for _, phone := range person.Phones {
		parts = append(parts, phone.Value)
	}
	for _, address := range person.Addresses {
		parts = append(parts, strings.Join(strings.Fields(strings.ReplaceAll(address.Value, "\n", ", ")), " "))
	}
	services := make([]string, 0, len(person.Accounts))
	for service := range person.Accounts {
		services = append(services, service)
	}
	sort.Strings(services)
	for _, service := range services {
		for _, value := range person.Accounts[service] {
			parts = append(parts, service+":"+value)
		}
	}
	parts = append(parts, person.Annotation)
	parts = append(parts, bodyWithoutHeadings(person.Body))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " · ")
}

func bodyWithoutHeadings(body string) string {
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func scoreText(text, query string) int {
	if text == query {
		return 100
	}
	return strings.Count(text, query)
}

func snippet(body, query string) string {
	lower := strings.ToLower(body)
	idx := strings.Index(lower, query)
	if idx < 0 {
		return ""
	}
	start := max(idx-40, 0)
	end := min(idx+len(query)+80, len(body))
	return strings.TrimSpace(body[start:end])
}
