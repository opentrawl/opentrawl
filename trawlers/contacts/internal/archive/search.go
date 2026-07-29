package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
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
			matches := personSearchMatches(person, query)
			hits = append(hits, scoredHit{AnchorID: personSearchAnchor(matches), PersonID: person.ID, Who: person.Name, Score: score, Snippet: personSnippet(person, query), Matches: matches})
		}
		notes, err := s.Notes(ctx, person.ID)
		if err != nil {
			return nil, 0, err
		}
		for _, note := range notes {
			text := strings.ToLower(strings.Join(append([]string{note.Kind, note.Source, note.Body}, note.Topics...), " "))
			if score := scoreText(text, query); score > 0 {
				hits = append(hits, scoredHit{AnchorID: NoteAnchorID(note.ID), PersonID: person.ID, Who: person.Name, AccountProviderName: note.Source, Score: score, Snippet: noteSnippet(note, query), Time: note.OccurredAt, Matches: noteSearchMatches(note, query)})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].PersonID < hits[j].PersonID
		}
		return hits[i].Score > hits[j].Score
	})
	resultCapacity := len(hits)
	if options.Limit > 0 && options.Limit < resultCapacity {
		resultCapacity = options.Limit
	}
	results := make([]SearchResult, 0, resultCapacity)
	total := 0
	for _, hit := range hits {
		if !withinRange(hit.Time, options.After, options.Before) {
			continue
		}
		total++
		if options.Limit > 0 && len(results) >= options.Limit {
			continue
		}
		person := byID[hit.PersonID]
		accountProviderName := strings.TrimSpace(hit.AccountProviderName)
		if accountProviderName == "" {
			accountProviderName = personSearchAccountProviderName(person)
		}
		ref := PersonRef(hit.PersonID)
		results = append(results, SearchResult{
			AnchorID:                   hit.AnchorID,
			Ref:                        ref,
			Time:                       hit.Time,
			Who:                        hit.Who,
			AlternativePersonNames:     resolverIdentityAliases(person),
			PersonTechnicalIdentifiers: resolverIdentifiers(person),
			Snippet:                    hit.Snippet,
			PersonID:                   hit.PersonID,
			PhysicalPlaceName:          personSearchPhysicalPlaceName(person),
			AccountProviderName:        accountProviderName,
			Matches:                    hit.Matches,
		})
	}
	return results, total, nil
}

type scoredHit struct {
	AnchorID            string
	PersonID            string
	Who                 string
	AccountProviderName string
	Score               int
	Snippet             string
	Time                time.Time
	Matches             []SearchMatch
}

func (s *Store) searchPeopleFTS(ctx context.Context, query string, people map[string]model.Person) ([]scoredHit, error) {
	match := ftsPrefixQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := s.database().QueryContext(ctx, `
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
		matches := personSearchMatches(person, query)
		hits = append(hits, scoredHit{AnchorID: personSearchAnchor(matches), PersonID: id, Who: person.Name, Score: 100, Snippet: personSnippet(person, query), Matches: matches})
	}
	return hits, rows.Err()
}

// NoteAnchorID returns the stable presentation anchor for a contact note.
func NoteAnchorID(noteID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(noteID)))
	return "note-" + hex.EncodeToString(sum[:8])
}

func personSearchAnchor(matches []SearchMatch) string {
	if len(matches) == 0 {
		return openrecord.PersonDisplayNameAnchorID
	}
	return matches[0].Field
}

func personSearchMatches(person model.Person, query string) []SearchMatch {
	values := []struct {
		field string
		value string
	}{
		{openrecord.PersonDisplayNameAnchorID, person.Name},
		{"sort_name", person.SortName},
		{"annotation", person.Annotation},
		{"body", person.Body},
		{"identifier", person.ID},
	}
	for _, value := range person.AKA {
		values = append(values, struct{ field, value string }{openrecord.PersonAlternativeDisplayNameAnchorID, value})
	}
	for _, value := range person.Tags {
		values = append(values, struct{ field, value string }{"tag", value})
	}
	for _, value := range person.Emails {
		values = append(values, struct{ field, value string }{openrecord.PersonEmailAddressAnchorID, value.Value})
	}
	for _, value := range person.Phones {
		values = append(values, struct{ field, value string }{openrecord.PersonPhoneNumberAnchorID, value.Value})
	}
	for _, value := range person.Addresses {
		values = append(values, struct{ field, value string }{openrecord.PersonPostalAddressAnchorID, value.Value})
	}
	for service, identifiers := range person.Accounts {
		humanAccountIdentifierExists := false
		for _, identifier := range identifiers {
			humanAccountIdentifier := model.AccountIdentifierForHumanPresentation(
				service,
				identifier,
			)
			if humanAccountIdentifier == "" {
				continue
			}
			humanAccountIdentifierExists = true
			values = append(values, struct{ field, value string }{openrecord.PersonAccountIdentifierAnchorID, service + ":" + humanAccountIdentifier})
		}
		if humanAccountIdentifierExists {
			values = append(values, struct{ field, value string }{openrecord.PersonAccountIdentifierAnchorID, service})
		}
	}
	for _, source := range person.Sources {
		for _, name := range source.Names {
			values = append(values, struct{ field, value string }{openrecord.PersonAlternativeDisplayNameAnchorID, name})
		}
	}
	return collectSearchMatches(values, query)
}

func noteSearchMatches(note model.Note, query string) []SearchMatch {
	values := []struct {
		field string
		value string
	}{{"note_kind", note.Kind}, {"note_source", note.Source}, {"note_body", note.Body}}
	for _, topic := range note.Topics {
		values = append(values, struct{ field, value string }{"note_topic", topic})
	}
	return collectSearchMatches(values, query)
}

func collectSearchMatches(values []struct{ field, value string }, query string) []SearchMatch {
	matches := make([]SearchMatch, 0, len(values))
	for _, value := range values {
		if runs := searchTextRuns(value.value, query); len(runs) > 0 {
			matches = append(matches, SearchMatch{Field: value.field, Runs: runs})
		}
	}
	return matches
}

func searchTextRuns(value, query string) []SearchTextRun {
	patterns := []string{strings.TrimSpace(query)}
	if !strings.Contains(strings.ToLower(value), strings.ToLower(patterns[0])) {
		patterns = strings.Fields(query)
	}
	type span struct{ start, end int }
	var spans []span
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(pattern))
		for _, match := range re.FindAllStringIndex(value, -1) {
			spans = append(spans, span{start: match[0], end: match[1]})
		}
	}
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := spans[:1]
	for _, current := range spans[1:] {
		last := &merged[len(merged)-1]
		if current.start <= last.end {
			if current.end > last.end {
				last.end = current.end
			}
			continue
		}
		merged = append(merged, current)
	}
	runs := make([]SearchTextRun, 0, len(merged)*2+1)
	position := 0
	for _, match := range merged {
		if position < match.start {
			runs = append(runs, SearchTextRun{Text: value[position:match.start]})
		}
		runs = append(runs, SearchTextRun{Text: value[match.start:match.end], Matched: true})
		position = match.end
	}
	if position < len(value) {
		runs = append(runs, SearchTextRun{Text: value[position:]})
	}
	return runs
}

func noteSnippet(note model.Note, query string) string {
	for _, match := range noteSearchMatches(note, query) {
		var value strings.Builder
		for _, run := range match.Runs {
			value.WriteString(run.Text)
		}
		if text := strings.TrimSpace(value.String()); text != "" {
			return text
		}
	}
	return note.Body
}

func withinRange(t, after, before time.Time) bool {
	if t.IsZero() {
		return after.IsZero() && before.IsZero()
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
		for _, value := range values {
			humanAccountIdentifier := model.AccountIdentifierForHumanPresentation(
				service,
				value,
			)
			if humanAccountIdentifier != "" {
				parts = append(parts, service, humanAccountIdentifier)
			}
		}
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
			humanAccountIdentifier := model.AccountIdentifierForHumanPresentation(
				service,
				value,
			)
			if humanAccountIdentifier != "" {
				parts = append(parts, service+":"+humanAccountIdentifier)
			}
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

func personSearchPhysicalPlaceName(person model.Person) string {
	for _, address := range person.Addresses {
		physicalPlaceName := strings.Join(strings.Fields(strings.ReplaceAll(address.Value, "\n", ", ")), " ")
		if physicalPlaceName != "" {
			return physicalPlaceName
		}
	}
	return ""
}

func personSearchAccountProviderName(person model.Person) string {
	accountProviderNames := make(map[string]struct{}, len(person.Accounts)+len(person.Sources)+2)
	for accountProviderName := range person.Accounts {
		accountProviderNames[strings.TrimSpace(accountProviderName)] = struct{}{}
	}
	for accountProviderName := range person.Sources {
		accountProviderNames[strings.TrimSpace(accountProviderName)] = struct{}{}
	}
	if strings.TrimSpace(person.Apple.ID) != "" {
		accountProviderNames["apple"] = struct{}{}
	}
	if strings.TrimSpace(person.Google.ID) != "" {
		accountProviderNames["google"] = struct{}{}
	}
	delete(accountProviderNames, "")
	namesInAlphabeticalOrder := make([]string, 0, len(accountProviderNames))
	for accountProviderName := range accountProviderNames {
		namesInAlphabeticalOrder = append(namesInAlphabeticalOrder, accountProviderName)
	}
	sort.Strings(namesInAlphabeticalOrder)
	if len(namesInAlphabeticalOrder) == 0 {
		return ""
	}
	return namesInAlphabeticalOrder[0]
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
