package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openclaw/clawdex/internal/contactexport"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/clawdex/internal/repo"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
	toml "github.com/pelletier/go-toml/v2"
)

// Envelopes shared by both output modes. JSON arrays are always present,
// never null; human mode renders through crawlkit render components.

type searchEnvelope struct {
	Query        string            `json:"query"`
	Results      []model.SearchHit `json:"results"`
	TotalMatches int               `json:"total_matches"`
	Truncated    bool              `json:"truncated"`

	limit int
}

type peopleEnvelope struct {
	Query     string         `json:"query,omitempty"`
	People    []model.Person `json:"people"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`

	limit int
}

type notesEnvelope struct {
	PersonID  string       `json:"person_id"`
	Person    string       `json:"person"`
	Notes     []model.Note `json:"notes"`
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated"`

	verb  string
	query string
	limit int
}

type importChangesEnvelope struct {
	Changes                   []model.ImportChange `json:"changes"`
	SkippedWithoutIdentifiers int                  `json:"skipped_without_identifiers,omitempty"`
}

func (r *Runtime) print(value any) error {
	if r.root.JSON {
		enc := json.NewEncoder(r.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err := fmt.Fprintf(r.stdout, "%s: %v\n", key, v[key]); err != nil {
				return err
			}
		}
		return nil
	case repo.Config:
		return printConfigTOML(r, v)
	case control.Manifest:
		return printManifest(r, v)
	case control.Status:
		return printStatusText(r.stdout, v, r.renderLogTail())
	case contactexport.ContactExport:
		return printContactExport(r, v)
	case model.Person:
		return printPersonCard(r, v)
	case model.AvatarRef:
		return printAvatar(r, v)
	case searchEnvelope:
		return printSearch(r, v)
	case peopleEnvelope:
		return printPeople(r, v)
	case notesEnvelope:
		return printNotes(r, v)
	case importChangesEnvelope:
		return printImportChanges(r, v)
	default:
		return fmt.Errorf("internal: no human renderer for %T", value)
	}
}

func printConfigTOML(r *Runtime, cfg repo.Config) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = r.stdout.Write(data)
	return err
}

func printManifest(r *Runtime, m control.Manifest) error {
	if _, err := fmt.Fprintf(r.stdout, "%s: %s\n", m.ID, m.Description); err != nil {
		return err
	}
	fields := []render.CardField{
		{Label: "version", Value: m.Version},
		{Label: "contacts repo", Value: m.Paths.DefaultDatabase},
		{Label: "config", Value: m.Paths.DefaultConfig},
		{Label: "logs", Value: m.Paths.DefaultLogs},
		{Label: "capabilities", Value: strings.Join(m.Capabilities, ", ")},
	}
	return render.WriteCard(r.stdout, render.Card{Fields: fields})
}

func printSearch(r *Runtime, v searchEnvelope) error {
	items := make([]render.ListItem, 0, len(v.Results))
	for _, hit := range v.Results {
		items = append(items, render.ListItem{
			Time: hit.Timestamp,
			Who:  hit.Name,
			Text: hit.Snippet,
		})
	}
	hints := []string{"Show a person: clawdex person show NAME (their notes: clawdex note list NAME)"}
	if v.Truncated {
		hints = append(hints, fmt.Sprintf("More: clawdex search %q --limit %d", v.Query, nextLimit(v.limit)))
	}
	return render.WriteList(r.stdout, render.List{
		Heading:   fmt.Sprintf("Search %q: showing %d of %d, best match first.", v.Query, len(v.Results), v.TotalMatches),
		Hints:     hints,
		Items:     items,
		ClampText: 2,
		Empty:     fmt.Sprintf("No matches for %q.", v.Query),
	})
}

func printPeople(r *Runtime, v peopleEnvelope) error {
	if len(v.People) == 0 {
		if v.Query != "" {
			_, err := fmt.Fprintf(r.stdout, "No people match %q.\n", v.Query)
			return err
		}
		_, err := fmt.Fprintln(r.stdout, "No people yet. Add one: clawdex person add NAME")
		return err
	}
	heading := fmt.Sprintf("People: showing %d of %d, A to Z.", len(v.People), v.Total)
	if v.Query != "" {
		heading = fmt.Sprintf("People matching %q: showing %d of %d, A to Z.", v.Query, len(v.People), v.Total)
	}
	if _, err := fmt.Fprintln(r.stdout, heading); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.stdout, "Show one: clawdex person show NAME"); err != nil {
		return err
	}
	if v.Truncated {
		more := fmt.Sprintf("More: clawdex person list --limit %d", nextLimit(v.limit))
		if v.Query != "" {
			more = fmt.Sprintf("More: clawdex person list --query %q --limit %d", v.Query, nextLimit(v.limit))
		}
		if _, err := fmt.Fprintln(r.stdout, more); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	anyTags := false
	for _, p := range v.People {
		if len(p.Tags) > 0 {
			anyTags = true
			break
		}
	}
	columns := []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "email"},
		{Header: "phone"},
	}
	if anyTags {
		columns = append(columns, render.TableColumn{Header: "tags", Wrap: true})
	}
	rows := make([][]string, 0, len(v.People))
	for _, p := range v.People {
		row := []string{p.Name, firstContactValue(p.Emails), firstContactValue(p.Phones)}
		if anyTags {
			row = append(row, strings.Join(p.Tags, ", "))
		}
		rows = append(rows, row)
	}
	return render.WriteTable(r.stdout, columns, rows)
}

func printNotes(r *Runtime, v notesEnvelope) error {
	items := make([]render.ListItem, 0, len(v.Notes))
	for _, n := range v.Notes {
		items = append(items, render.ListItem{
			Time:  n.OccurredAt,
			Where: n.Kind,
			Text:  n.Body,
		})
	}
	hints := []string{fmt.Sprintf("Add one: clawdex note add %q --kind note --source manual --text TEXT", v.query)}
	if v.Truncated {
		hints = append(hints, fmt.Sprintf("More: clawdex %s %q --limit %d", v.verb, v.query, nextLimit(v.limit)))
	}
	return render.WriteList(r.stdout, render.List{
		Heading:   fmt.Sprintf("Notes for %s: showing %d of %d, newest first.", v.Person, len(v.Notes), v.Total),
		Hints:     hints,
		Items:     items,
		ClampText: 0,
		Empty:     fmt.Sprintf("No notes for %s. Add one: clawdex note add %q --kind note --source manual --text TEXT", v.Person, v.query),
	})
}

func printPersonCard(r *Runtime, p model.Person) error {
	fields := []render.CardField{
		{Label: "id", Value: p.ID},
		{Label: "aka", Value: strings.Join(p.AKA, ", ")},
		{Label: "tags", Value: strings.Join(p.Tags, ", ")},
		{Label: "email", Value: joinContactValues(p.Emails)},
		{Label: "phone", Value: joinContactValues(p.Phones)},
		{Label: "address", Value: joinAddresses(p.Addresses)},
		{Label: "sources", Value: strings.Join(sortedSourceNames(p), ", ")},
		{Label: "file", Value: p.Path},
	}
	return render.WriteCard(r.stdout, render.Card{
		Title:  p.Name,
		Fields: fields,
		Hints:  []string{fmt.Sprintf("Notes: clawdex note list %q", p.Name)},
	})
}

func printAvatar(r *Runtime, a model.AvatarRef) error {
	fields := []render.CardField{
		{Label: "file", Value: a.Path},
		{Label: "type", Value: a.MIME},
		{Label: "source", Value: a.Source},
	}
	if a.Width > 0 && a.Height > 0 {
		fields = append(fields, render.CardField{Label: "size", Value: fmt.Sprintf("%dx%d", a.Width, a.Height)})
	}
	if !a.UpdatedAt.IsZero() {
		fields = append(fields, render.CardField{Label: "updated", Value: render.ShortLocalTime(a.UpdatedAt)})
	}
	return render.WriteCard(r.stdout, render.Card{Title: "Avatar", Fields: fields})
}

func printContactExport(r *Runtime, export contactexport.ContactExport) error {
	if len(export.Contacts) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No contacts to export.")
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "Contact export: %s, A to Z.\n\n", countNoun(len(export.Contacts), "contact", "contacts")); err != nil {
		return err
	}
	rows := make([][]string, 0, len(export.Contacts))
	for _, contact := range export.Contacts {
		rows = append(rows, []string{
			contact.DisplayName,
			countNoun(contactIdentifierCount(contact), "identifier", "identifiers"),
			countNoun(len(contact.Addresses), "address", "addresses"),
		})
	}
	return render.WriteTable(r.stdout, []render.TableColumn{
		{Header: "who", Wrap: true},
		{Header: "identifiers"},
		{Header: "addresses"},
	}, rows)
}

func printImportChanges(r *Runtime, v importChangesEnvelope) error {
	suffix := "."
	if r.root.DryRun {
		suffix = " (dry run, nothing written)."
	}
	if len(v.Changes) == 0 {
		if _, err := fmt.Fprintf(r.stdout, "No contact changes%s\n", suffix); err != nil {
			return err
		}
		return printImportSkipped(r, v.SkippedWithoutIdentifiers)
	}
	if _, err := fmt.Fprintf(r.stdout, "Import: %s%s\n\n", countNoun(len(v.Changes), "contact change", "contact changes"), suffix); err != nil {
		return err
	}
	rows := make([][]string, 0, len(v.Changes))
	for _, change := range v.Changes {
		rows = append(rows, []string{change.Action, change.Name, change.PersonID})
	}
	if err := render.WriteTable(r.stdout, []render.TableColumn{
		{Header: "action"},
		{Header: "who", Wrap: true},
		{Header: "id"},
	}, rows); err != nil {
		return err
	}
	return printImportSkipped(r, v.SkippedWithoutIdentifiers)
}

func printImportSkipped(r *Runtime, skipped int) error {
	if skipped <= 0 {
		return nil
	}
	_, err := fmt.Fprintf(r.stdout, "Skipped %s with no email, phone or handle.\n", countNoun(skipped, "contact", "contacts"))
	return err
}

func newImportChangesEnvelope(changes []model.ImportChange, skipped int) importChangesEnvelope {
	if changes == nil {
		changes = []model.ImportChange{}
	}
	return importChangesEnvelope{Changes: changes, SkippedWithoutIdentifiers: skipped}
}

func firstContactValue(values []model.ContactValue) string {
	if len(values) == 0 {
		return ""
	}
	return values[0].Value
}

func joinContactValues(values []model.ContactValue) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if v := strings.TrimSpace(value.Value); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

func joinAddresses(values []model.ContactValue) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.Join(strings.Fields(strings.ReplaceAll(value.Value, "\n", ", ")), " ")
		if v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, "; ")
}

func sortedSourceNames(p model.Person) []string {
	out := make([]string, 0, len(p.Sources))
	for source := range p.Sources {
		if source = strings.TrimSpace(source); source != "" {
			out = append(out, source)
		}
	}
	sort.Strings(out)
	return out
}

func contactIdentifierCount(contact contactexport.Contact) int {
	return len(contact.PhoneNumbers) +
		len(contact.Emails) +
		contactAccountValueCount(contact.Accounts) +
		contactAccountValueCount(contact.Handles)
}

func contactAccountValueCount(accounts map[string][]string) int {
	count := 0
	for _, values := range accounts {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				count++
			}
		}
	}
	return count
}

func nextLimit(limit int) int {
	if limit < 1 {
		limit = 1
	}
	return limit * 2
}
