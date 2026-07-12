package clawdex

import (
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	contactsopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/contacts/open/v1"
)

func projectOpenRecord(ref string, value model.Person) *contactsopenv1.ContactsRecord {
	record := &contactsopenv1.ContactsRecord{
		Ref:       ref,
		Name:      value.Name,
		Aka:       append([]string(nil), value.AKA...),
		Tags:      append([]string(nil), value.Tags...),
		Emails:    projectContactValues(value.Emails),
		Phones:    projectContactValues(value.Phones),
		Addresses: projectContactValues(value.Addresses),
		Accounts:  make(map[string]*contactsopenv1.IdentifierList, len(value.Accounts)),
	}
	if record.Ref == "" {
		record.Ref = archive.PersonRef(value.ID)
	}
	setOptionalString(&record.SortName, value.SortName)
	for name, identifiers := range value.Accounts {
		record.Accounts[name] = &contactsopenv1.IdentifierList{Values: append([]string(nil), identifiers...)}
	}
	setOptionalString(&record.Annotation, value.Annotation)
	setOptionalString(&record.AnnotationStatedAt, value.AnnotationStatedAt)
	return record
}

func projectContactValues(values []model.ContactValue) []*contactsopenv1.ContactValue {
	records := make([]*contactsopenv1.ContactValue, 0, len(values))
	for _, value := range values {
		record := &contactsopenv1.ContactValue{Value: value.Value}
		setOptionalString(&record.Label, value.Label)
		if value.Primary {
			record.Primary = recordBool(true)
		}
		records = append(records, record)
	}
	return records
}

func setOptionalString(target **string, value string) {
	if value != "" {
		*target = &value
	}
}

func recordBool(value bool) *bool { return &value }

func projectOpenPresentation(ref string, value model.Person) *presentationv1.PresentationDocument {
	record := projectOpenRecord(ref, value)
	title := strings.TrimSpace(record.Name)
	if title == "" {
		title = "Contact"
	}
	fields := []*presentationv1.Field{{Label: "Ref", Display: record.Ref}}
	appendPresentationField(&fields, "Also known as", joinPresentationStrings(record.Aka))
	appendPresentationField(&fields, "Tags", joinPresentationStrings(record.Tags))
	appendPresentationField(&fields, "Emails", formatPresentationContactValues(record.Emails))
	appendPresentationField(&fields, "Phones", formatPresentationContactValues(record.Phones))
	appendPresentationField(&fields, "Addresses", formatPresentationContactValues(record.Addresses))
	appendPresentationField(&fields, "Accounts", formatPresentationAccounts(record.Accounts))
	appendPresentationField(&fields, "Annotation", record.GetAnnotation())
	return &presentationv1.PresentationDocument{Title: title, Blocks: []*presentationv1.Block{{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}}}}
}

func appendPresentationField(fields *[]*presentationv1.Field, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value})
	}
}

func joinPresentationStrings(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return strings.Join(items, ", ")
}

func formatPresentationContactValues(values []*contactsopenv1.ContactValue) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil || strings.TrimSpace(value.Value) == "" {
			continue
		}
		item := strings.TrimSpace(value.Value)
		if label := strings.TrimSpace(value.GetLabel()); label != "" {
			item += " (" + label + ")"
		}
		if value.GetPrimary() {
			item += " [primary]"
		}
		items = append(items, item)
	}
	return strings.Join(items, ", ")
}

func formatPresentationAccounts(values map[string]*contactsopenv1.IdentifierList) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]string, 0, len(keys))
	for _, key := range keys {
		list := values[key]
		if list == nil {
			continue
		}
		identifiers := make([]string, 0, len(list.Values))
		for _, value := range list.Values {
			if value = strings.TrimSpace(value); value != "" {
				identifiers = append(identifiers, value)
			}
		}
		if len(identifiers) != 0 {
			items = append(items, strings.TrimSpace(key)+": "+strings.Join(identifiers, ", "))
		}
	}
	return strings.Join(items, "; ")
}
