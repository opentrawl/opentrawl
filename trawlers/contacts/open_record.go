package clawdex

import (
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
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
