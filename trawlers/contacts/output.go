package contacts

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type peopleEnvelope struct {
	Query     string         `json:"query,omitempty"`
	People    []model.Person `json:"people"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`

	limit int
}

type publicContactValue struct {
	Value   string `json:"value"`
	Label   string `json:"label,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

type publicPerson struct {
	Ref                string               `json:"ref"`
	Name               string               `json:"name"`
	SortName           string               `json:"sort_name,omitempty"`
	AKA                []string             `json:"aka,omitempty"`
	Tags               []string             `json:"tags,omitempty"`
	Emails             []publicContactValue `json:"emails,omitempty"`
	Phones             []publicContactValue `json:"phones,omitempty"`
	Addresses          []publicContactValue `json:"addresses,omitempty"`
	Accounts           map[string][]string  `json:"accounts,omitempty"`
	Annotation         string               `json:"annotation,omitempty"`
	AnnotationStatedAt string               `json:"annotation_stated_at,omitempty"`
}

type publicPeopleEnvelope struct {
	Query     string         `json:"query,omitempty"`
	People    []publicPerson `json:"people"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`
}

func writePeople(req *trawlkit.Request, value peopleEnvelope) error {
	if value.People == nil {
		value.People = []model.Person{}
	}
	if req.Format == ckoutput.JSON {
		people := make([]publicPerson, 0, len(value.People))
		for _, person := range value.People {
			people = append(people, projectPublicPerson(person))
		}
		return ckoutput.Write(req.Out, req.Format, "people", publicPeopleEnvelope{
			Query: value.Query, People: people, Total: value.Total, Truncated: value.Truncated,
		})
	}
	if len(value.People) == 0 {
		if value.Query != "" {
			_, err := fmt.Fprintf(req.Out, "No people match %q.\n", value.Query)
			return err
		}
		_, err := fmt.Fprintln(req.Out, "No people yet.")
		return err
	}
	heading := fmt.Sprintf("People: showing %s of %s, A to Z.", render.FormatInteger(int64(len(value.People))), render.FormatInteger(int64(value.Total)))
	if value.Query != "" {
		heading = fmt.Sprintf("People matching %q: showing %s of %s, A to Z.", value.Query, render.FormatInteger(int64(len(value.People))), render.FormatInteger(int64(value.Total)))
	}
	if _, err := fmt.Fprintln(req.Out, heading); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(req.Out, "Show one: trawl contacts person show NAME"); err != nil {
		return err
	}
	if value.Truncated {
		more := fmt.Sprintf("More: trawl contacts person list --limit %d", value.limit*2)
		if value.Query != "" {
			more = fmt.Sprintf("More: trawl contacts person list --query %q --limit %d", value.Query, value.limit*2)
		}
		if _, err := fmt.Fprintln(req.Out, more); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(req.Out); err != nil {
		return err
	}
	columns := []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "email"},
		{Header: "phone"},
	}
	if peopleHaveTags(value.People) {
		columns = append(columns, render.TableColumn{Header: "tags", Wrap: true})
	}
	rows := make([][]string, 0, len(value.People))
	for _, person := range value.People {
		row := []string{person.Name, firstContactValue(person.Emails), render.FormatPhone(firstContactValue(person.Phones))}
		if peopleHaveTags(value.People) {
			row = append(row, strings.Join(person.Tags, ", "))
		}
		rows = append(rows, row)
	}
	return render.WriteTable(req.Out, columns, rows)
}

func writePerson(req *trawlkit.Request, person model.Person) error {
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "person", projectPublicPerson(person))
	}
	return render.WriteCard(req.Out, render.Card{
		Title: person.Name,
		Fields: []render.CardField{
			{Label: "id", Value: person.ID},
			{Label: "aka", Value: strings.Join(person.AKA, ", ")},
			{Label: "tags", Value: strings.Join(person.Tags, ", ")},
			{Label: "email", Value: joinContactValues(person.Emails)},
			{Label: "phone", Value: joinPhoneValues(person.Phones)},
			{Label: "address", Value: joinAddresses(person.Addresses)},
			{Label: "sources", Value: strings.Join(sortedSourceNames(person), ", ")},
			{Label: "annotation", Value: person.Annotation},
			{Label: "stated", Value: person.AnnotationStatedAt},
		},
	})
}

func writePersonAnnotation(req *trawlkit.Request, person model.Person) error {
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "annotation", projectPublicPerson(person))
	}
	return render.WriteCard(req.Out, render.Card{
		Title: "Person annotation recorded",
		Fields: []render.CardField{
			{Label: "Person", Value: person.Name},
			{Label: "Annotation", Value: person.Annotation},
			{Label: "Stated", Value: person.AnnotationStatedAt},
		},
	})
}

func projectPublicPerson(person model.Person) publicPerson {
	return publicPerson{
		Ref:                archive.PersonRef(person.ID),
		Name:               person.Name,
		SortName:           person.SortName,
		AKA:                append([]string(nil), person.AKA...),
		Tags:               append([]string(nil), person.Tags...),
		Emails:             projectPublicContactValues(person.Emails),
		Phones:             projectPublicContactValues(person.Phones),
		Addresses:          projectPublicContactValues(person.Addresses),
		Accounts:           copyAccounts(person.Accounts),
		Annotation:         person.Annotation,
		AnnotationStatedAt: person.AnnotationStatedAt,
	}
}

func projectPublicContactValues(values []model.ContactValue) []publicContactValue {
	if len(values) == 0 {
		return nil
	}
	out := make([]publicContactValue, 0, len(values))
	for _, value := range values {
		out = append(out, publicContactValue{Value: value.Value, Label: value.Label, Primary: value.Primary})
	}
	return out
}

func copyAccounts(accounts map[string][]string) map[string][]string {
	if len(accounts) == 0 {
		return nil
	}
	out := make(map[string][]string, len(accounts))
	for provider, values := range accounts {
		out[provider] = append([]string(nil), values...)
	}
	return out
}

func peopleHaveTags(people []model.Person) bool {
	for _, person := range people {
		if len(person.Tags) > 0 {
			return true
		}
	}
	return false
}

func firstContactValue(values []model.ContactValue) string {
	for _, value := range values {
		if strings.TrimSpace(value.Value) != "" {
			return strings.TrimSpace(value.Value)
		}
	}
	return ""
}

func joinContactValues(values []model.ContactValue) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Value) != "" {
			out = append(out, strings.TrimSpace(value.Value))
		}
	}
	return strings.Join(out, ", ")
}

func joinPhoneValues(values []model.ContactValue) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Value) != "" {
			out = append(out, render.FormatPhone(value.Value))
		}
	}
	return strings.Join(out, ", ")
}

func joinAddresses(values []model.ContactValue) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		address := strings.Join(strings.Fields(strings.ReplaceAll(value.Value, "\n", ", ")), " ")
		if address != "" {
			out = append(out, address)
		}
	}
	return strings.Join(out, "; ")
}

func sortedSourceNames(person model.Person) []string {
	names := make([]string, 0, len(person.Sources))
	for name := range person.Sources {
		if strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	sort.Strings(names)
	return names
}
