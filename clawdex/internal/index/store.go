package index

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/markdown"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/clawdex/internal/repo"
)

type Store struct {
	Repo repo.Repo
	Log  io.Writer
}

func New(r repo.Repo) Store {
	return Store{Repo: r}
}

func NewWithLog(r repo.Repo, log io.Writer) Store {
	return Store{Repo: r, Log: log}
}

func (s Store) AddPerson(name string, emails, phones, tags []string, now time.Time) (model.Person, error) {
	p := markdown.NewPerson(name, now)
	p.Tags = cleanList(tags)
	for i, email := range cleanList(emails) {
		p.Emails = append(p.Emails, model.ContactValue{Value: email, Label: "other", Source: "manual", Primary: i == 0})
	}
	for i, phone := range cleanList(phones) {
		p.Phones = append(p.Phones, model.ContactValue{Value: phone, Label: "other", Source: "manual", Primary: i == 0})
	}
	dir, err := s.uniquePersonDir(model.Slug(name))
	if err != nil {
		return model.Person{}, err
	}
	path := filepath.Join(dir, "person.md")
	p.Path = path
	p.Body = "# " + p.Name + "\n"
	if err := markdown.WritePerson(path, p); err != nil {
		return model.Person{}, err
	}
	return p, s.Rebuild()
}

func (s Store) People() ([]model.Person, error) {
	if _, _, err := s.ensureIndex(); err != nil {
		return nil, err
	}
	return s.readPeople()
}

func (s Store) readPeople() ([]model.Person, error) {
	if err := s.Repo.Require(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Repo.PeopleDir())
	if err != nil {
		return nil, err
	}
	people := make([]model.Person, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(s.Repo.PeopleDir(), entry.Name(), "person.md")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		p, _, err := markdown.ReadPerson(path)
		if err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	sort.Slice(people, func(i, j int) bool {
		return strings.ToLower(people[i].Name) < strings.ToLower(people[j].Name)
	})
	return people, nil
}

func (s Store) FindPerson(query string) (model.Person, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return model.Person{}, errors.New("person query is required")
	}
	ids, err := s.personIDsForQuery(query)
	if err != nil {
		return model.Person{}, err
	}
	if len(ids) == 0 {
		return model.Person{}, fmt.Errorf("no person matched %q", query)
	}
	people, err := s.readPeople()
	if err != nil {
		return model.Person{}, err
	}
	byID := peopleByID(people)
	matches := make([]model.Person, 0, len(ids))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			matches = append(matches, p)
		}
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, match := range matches {
			names = append(names, match.Name+" ("+match.ID+")")
		}
		return model.Person{}, fmt.Errorf("ambiguous person %q: %s", query, strings.Join(names, ", "))
	}
	return matches[0], nil
}

func (s Store) AddNote(personQuery string, note model.Note) (model.Note, error) {
	p, err := s.FindPerson(personQuery)
	if err != nil {
		return model.Note{}, err
	}
	note.PersonID = p.ID
	dir := filepath.Join(filepath.Dir(p.Path), "notes")
	path := filepath.Join(dir, markdown.NoteFileName(note))
	for i := 2; ; i++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			break
		}
		ext := filepath.Ext(path)
		base := strings.TrimSuffix(path, ext)
		path = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	note.Path = path
	if err := markdown.WriteNote(path, note); err != nil {
		return model.Note{}, err
	}
	return note, nil
}

func (s Store) Notes(personQuery string) (model.Person, []model.Note, error) {
	p, err := s.FindPerson(personQuery)
	if err != nil {
		return model.Person{}, nil, err
	}
	notes, err := s.notesForPerson(p)
	return p, notes, err
}

func (s Store) notesForPerson(p model.Person) ([]model.Note, error) {
	dir := filepath.Join(filepath.Dir(p.Path), "notes")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	notes := make([]model.Note, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		n, _, err := markdown.ReadNote(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if n.PersonID == "" {
			n.PersonID = p.ID
		}
		notes = append(notes, n)
	}
	sort.Slice(notes, func(i, j int) bool {
		return notes[i].OccurredAt.Before(notes[j].OccurredAt)
	})
	return notes, nil
}

func (s Store) Search(query string) ([]model.SearchHit, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, errors.New("search query is required")
	}
	if _, _, err := s.ensureIndex(); err != nil {
		return nil, err
	}
	people, err := s.readPeople()
	if err != nil {
		return nil, err
	}
	var hits []model.SearchHit
	personHitIDs := map[string]bool{}
	indexHits, err := s.searchPersonIndex(query)
	if err != nil {
		return nil, err
	}
	for _, hit := range indexHits {
		personHitIDs[hit.ID] = true
		hits = append(hits, hit)
	}
	for _, p := range people {
		text := personSearchText(p)
		if score := scoreText(text, query); score > 0 && !personHitIDs[p.ID] {
			hits = append(hits, model.SearchHit{Kind: "person", ID: p.ID, Name: p.Name, Path: p.Path, Score: score, Snippet: personSnippet(p, query)})
		}
		notes, err := s.notesForPerson(p)
		if err != nil {
			return nil, err
		}
		for _, n := range notes {
			text := strings.ToLower(strings.Join(append([]string{n.Kind, n.Source, n.Body}, n.Topics...), " "))
			if score := scoreText(text, query); score > 0 {
				hits = append(hits, model.SearchHit{Kind: "note", ID: n.ID, PersonID: p.ID, Name: p.Name, Path: n.Path, Score: score, Snippet: snippet(n.Body, query), Timestamp: n.OccurredAt})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Score > hits[j].Score
	})
	return hits, nil
}

func (s Store) Rebuild() error {
	people, err := s.readPeople()
	if err != nil {
		return err
	}
	fp, err := s.markdownFingerprint()
	if err != nil {
		return err
	}
	_, err = s.rebuildIndex(people, fp)
	return err
}

func peopleByID(people []model.Person) map[string]model.Person {
	out := make(map[string]model.Person, len(people))
	for _, p := range people {
		out[p.ID] = p
	}
	return out
}

func (s Store) uniquePersonDir(slug string) (string, error) {
	if slug == "" {
		slug = "person"
	}
	for i := 0; ; i++ {
		candidate := slug
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", slug, i+1)
		}
		path := filepath.Join(s.Repo.PeopleDir(), candidate)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
	}
}

func cleanList(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func personHasEmail(p model.Person, email string) bool {
	for _, v := range p.Emails {
		if model.NormalizeEmail(v.Value) == email {
			return true
		}
	}
	return false
}

func personHasPhone(p model.Person, phone string) bool {
	for _, v := range p.Phones {
		if model.NormalizePhone(v.Value) == phone {
			return true
		}
	}
	return false
}

// personSnippet is what a person hit shows in search results: the matched
// fragment of the person's card, so a reader sees why the row is here. The
// name is deliberately left out — it has its own column — so a name match
// falls back to the head of the card (identifiers, tags) instead of
// repeating the name.
func personSnippet(p model.Person, query string) string {
	text := personDisplayText(p)
	if s := snippet(text, query); s != "" {
		return s
	}
	if text != "" {
		return headOfText(text, 120)
	}
	return p.Name
}

func headOfText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func personDisplayText(p model.Person) string {
	parts := []string{}
	parts = append(parts, p.Tags...)
	for _, email := range p.Emails {
		parts = append(parts, email.Value)
	}
	for _, phone := range p.Phones {
		parts = append(parts, phone.Value)
	}
	for _, address := range p.Addresses {
		parts = append(parts, strings.Join(strings.Fields(strings.ReplaceAll(address.Value, "\n", ", ")), " "))
	}
	services := make([]string, 0, len(p.Accounts))
	for service := range p.Accounts {
		services = append(services, service)
	}
	sort.Strings(services)
	for _, service := range services {
		for _, value := range p.Accounts[service] {
			parts = append(parts, service+":"+value)
		}
	}
	parts = append(parts, bodyWithoutHeadings(p.Body))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " · ")
}

// bodyWithoutHeadings drops markdown heading lines: every person file body
// starts with "# Name", which would repeat the name in every snippet.
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

func personSearchText(p model.Person) string {
	parts := []string{p.ID, p.Name, p.SortName, p.Body}
	parts = append(parts, p.Tags...)
	for _, email := range p.Emails {
		parts = append(parts, email.Value)
	}
	for _, phone := range p.Phones {
		parts = append(parts, phone.Value)
	}
	for _, address := range p.Addresses {
		parts = append(parts, address.Value)
	}
	for service, values := range p.Accounts {
		parts = append(parts, service)
		parts = append(parts, values...)
	}
	return strings.ToLower(strings.Join(parts, " "))
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
	start := idx - 40
	start = max(start, 0)
	end := idx + len(query) + 80
	end = min(end, len(body))
	return strings.TrimSpace(body[start:end])
}
