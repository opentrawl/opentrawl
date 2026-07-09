package clawdex

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/repo"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	toml "github.com/pelletier/go-toml/v2"
)

type peopleEnvelope struct {
	Query     string         `json:"query,omitempty"`
	People    []model.Person `json:"people"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`

	limit int
}

type importChangesEnvelope struct {
	Changes []model.ImportChange `json:"changes"`
}

func writeMap(req *trawlkit.Request, value map[string]any) error {
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "result", value)
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(req.Out, "%s: %v\n", key, value[key]); err != nil {
			return err
		}
	}
	return nil
}

func writeConfig(req *trawlkit.Request, cfg repo.Config) error {
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "config", cfg)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = req.Out.Write(data)
	return err
}

func writePeople(req *trawlkit.Request, value peopleEnvelope) error {
	if value.People == nil {
		value.People = []model.Person{}
	}
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "people", value)
	}
	if len(value.People) == 0 {
		if value.Query != "" {
			_, err := fmt.Fprintf(req.Out, "No people match %q.\n", value.Query)
			return err
		}
		_, err := fmt.Fprintln(req.Out, "No people yet. Import some: trawl contacts import --help")
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
		return ckoutput.Write(req.Out, req.Format, "person", person)
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
			{Label: "file", Value: person.Path},
		},
	})
}

func writeImportChanges(req *trawlkit.Request, value importChangesEnvelope) error {
	if value.Changes == nil {
		value.Changes = []model.ImportChange{}
	}
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "import", value)
	}
	if len(value.Changes) == 0 {
		_, err := fmt.Fprintln(req.Out, "No contact changes.")
		return err
	}
	rows := make([][]string, 0, len(value.Changes))
	for _, change := range value.Changes {
		rows = append(rows, []string{
			change.Action,
			change.Name,
			change.Source.Source,
			firstImportIdentifier(change.Source),
		})
	}
	if _, err := fmt.Fprintf(req.Out, "Import: %s.\n\n", countNoun(len(value.Changes), "contact change", "contact changes")); err != nil {
		return err
	}
	return render.WriteTable(req.Out, []render.TableColumn{
		{Header: "action"},
		{Header: "who", Wrap: true},
		{Header: "source"},
		{Header: "identifier", Wrap: true},
	}, rows)
}

func writeDoctor(req *trawlkit.Request, doctor *trawlkit.Doctor) error {
	if doctor == nil {
		doctor = &trawlkit.Doctor{Checks: []trawlkit.Check{}}
	}
	if doctor.Checks == nil {
		doctor.Checks = []trawlkit.Check{}
	}
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "repair", doctor)
	}
	checks := make([]render.Check, 0, len(doctor.Checks))
	for _, check := range doctor.Checks {
		checks = append(checks, render.Check{
			Name:    check.ID,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return render.WriteDoctor(req.Out, checks, render.LogTail{})
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

func firstImportIdentifier(contact model.SourceContact) string {
	for _, email := range contact.Emails {
		if strings.TrimSpace(email.Value) != "" {
			return strings.TrimSpace(email.Value)
		}
	}
	for _, phone := range contact.Phones {
		if strings.TrimSpace(phone.Value) != "" {
			return render.FormatPhone(phone.Value)
		}
	}
	services := make([]string, 0, len(contact.Accounts))
	for service := range contact.Accounts {
		services = append(services, service)
	}
	sort.Strings(services)
	for _, service := range services {
		for _, value := range contact.Accounts[service] {
			if strings.TrimSpace(value) != "" {
				return service + ":" + strings.TrimSpace(value)
			}
		}
	}
	return contact.ExternalID
}

func countNoun(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%s %s", render.FormatInteger(int64(count)), plural)
}
