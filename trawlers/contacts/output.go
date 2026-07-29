package contacts

import (
	"sort"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type personListResponseValues struct {
	peopleInDisplayOrder     []model.Person
	totalMatchingPersonCount int
	moreMatchingPeopleExist  bool
}

func personListCommandResponse(
	personListValues personListResponseValues,
) (*commandv1.TrawlerCommandResponse, error) {
	personRecords := make(
		[]*personv1.PersonRecord,
		0,
		len(personListValues.peopleInDisplayOrder),
	)
	for _, person := range personListValues.peopleInDisplayOrder {
		personRecords = append(personRecords, personRecord(person))
	}
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_PersonListResponse{
			PersonListResponse: &personv1.PersonListResponse{
				PersonRecordsInDisplayOrder: personRecords,
				TotalMatchingPersonCount:    uint64(personListValues.totalMatchingPersonCount),
				MoreMatchingPeopleExist:     personListValues.moreMatchingPeopleExist,
			},
		},
	}, nil
}

func personRecord(person model.Person) *personv1.PersonRecord {
	personDisplayName := personHumanName(person)
	if personDisplayName == "" {
		personDisplayName = "Contact"
	}
	return &personv1.PersonRecord{
		CanonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment: archive.PersonRef(person.ID),
		PersonDisplayName:                         personDisplayName,
		AlternativePersonDisplayNames:             personKnownAs(person, personDisplayName),
		PersonContactMethodsInDisplayOrder:        personContactMethods(person),
		PersonFactContributingTrawlerDisplayNames: sortedSourceNames(person),
	}
}

func personContactMethods(person model.Person) []*personv1.PersonContactMethod {
	personContactMethods := make(
		[]*personv1.PersonContactMethod,
		0,
		len(person.Emails)+len(person.Phones)+len(person.Addresses)+len(person.Accounts),
	)
	for _, emailAddress := range person.Emails {
		emailAddressDisplayValue := strings.TrimSpace(emailAddress.Value)
		if emailAddressDisplayValue == "" {
			continue
		}
		personContactMethods = append(personContactMethods, &personv1.PersonContactMethod{
			PersonContactMethodKind:         personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_EMAIL_ADDRESS,
			PersonContactMethodLabel:        humanContactMethodLabel(emailAddress.Label, "email"),
			PersonContactMethodDisplayValue: emailAddressDisplayValue,
		})
	}
	for _, phoneNumber := range person.Phones {
		if strings.TrimSpace(phoneNumber.Value) == "" {
			continue
		}
		personContactMethods = append(personContactMethods, &personv1.PersonContactMethod{
			PersonContactMethodKind:         personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_PHONE_NUMBER,
			PersonContactMethodLabel:        humanContactMethodLabel(phoneNumber.Label, "phone"),
			PersonContactMethodDisplayValue: render.FormatPhone(phoneNumber.Value),
		})
	}
	for _, postalAddress := range person.Addresses {
		postalAddressDisplayValue := postalAddressForDisplay(postalAddress.Value)
		if postalAddressDisplayValue == "" {
			continue
		}
		personContactMethods = append(personContactMethods, &personv1.PersonContactMethod{
			PersonContactMethodKind:         personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_POSTAL_ADDRESS,
			PersonContactMethodLabel:        humanContactMethodLabel(postalAddress.Label, "address"),
			PersonContactMethodDisplayValue: postalAddressDisplayValue,
		})
	}
	accountServiceNames := make([]string, 0, len(person.Accounts))
	for accountServiceName := range person.Accounts {
		accountServiceNames = append(accountServiceNames, accountServiceName)
	}
	sort.Strings(accountServiceNames)
	for _, accountServiceName := range accountServiceNames {
		for _, accountIdentifier := range person.Accounts[accountServiceName] {
			accountIdentifier = model.AccountIdentifierForHumanPresentation(
				accountServiceName,
				accountIdentifier,
			)
			if accountIdentifier == "" {
				continue
			}
			personContactMethods = append(personContactMethods, &personv1.PersonContactMethod{
				PersonContactMethodKind:         personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_ACCOUNT_IDENTIFIER,
				PersonContactMethodLabel:        strings.TrimSpace(accountServiceName),
				PersonContactMethodDisplayValue: accountIdentifier,
			})
		}
	}
	return personContactMethods
}

func peopleInHumanDisplayOrder(people []model.Person) []model.Person {
	orderedPeople := append([]model.Person(nil), people...)
	sort.SliceStable(orderedPeople, func(left, right int) bool {
		leftDisplayName := personHumanName(orderedPeople[left])
		rightDisplayName := personHumanName(orderedPeople[right])
		if leftDisplayName == "" || rightDisplayName == "" {
			return leftDisplayName != "" && rightDisplayName == ""
		}
		return strings.ToLower(leftDisplayName) < strings.ToLower(rightDisplayName)
	})
	return orderedPeople
}

func humanContactMethodLabel(sourceLabel string, contactMethodKindDisplayName string) string {
	sourceLabel = strings.TrimSpace(sourceLabel)
	switch strings.ToLower(sourceLabel) {
	case "", "other", contactMethodKindDisplayName:
		return contactMethodKindDisplayName
	default:
		return sourceLabel
	}
}

func personHumanName(person model.Person) string {
	alternativePersonNames := append([]string(nil), person.AKA...)
	alternativePersonNames = append(alternativePersonNames, sortedSourceObservedPersonNames(person)...)
	return humanReadablePersonDisplayName(person.Name, alternativePersonNames, personMachineIdentifiers(person))
}

func sortedSourceObservedPersonNames(person model.Person) []string {
	sourceObservedPersonNames := []string{}
	for _, personSource := range person.Sources {
		sourceObservedPersonNames = append(sourceObservedPersonNames, personSource.Names...)
	}
	sort.Strings(sourceObservedPersonNames)
	return sourceObservedPersonNames
}

func personKnownAs(person model.Person, displayName string) []string {
	alternativePersonDisplayNames := append([]string(nil), person.AKA...)
	alternativePersonDisplayNames = append(
		alternativePersonDisplayNames,
		sortedSourceObservedPersonNames(person)...,
	)
	return humanReadableAlternativePersonDisplayNames(
		person.Name,
		alternativePersonDisplayNames,
		displayName,
		personMachineIdentifiers(person),
	)
}

func humanReadablePersonDisplayName(primaryName string, alternativeNames, technicalIdentifiers []string) string {
	for _, value := range append([]string{primaryName}, alternativeNames...) {
		value = strings.Join(strings.Fields(value), " ")
		if personDisplayNameIsHumanReadable(value, technicalIdentifiers) {
			return value
		}
	}
	return ""
}

func humanReadableAlternativePersonDisplayNames(
	primaryName string,
	alternativeNames []string,
	displayName string,
	technicalIdentifiers []string,
) []string {
	aliases := make([]string, 0, len(alternativeNames))
	seen := map[string]struct{}{strings.ToLower(displayName): {}}
	for _, value := range append([]string{primaryName}, alternativeNames...) {
		value = strings.Join(strings.Fields(value), " ")
		key := strings.ToLower(value)
		if !personDisplayNameIsHumanReadable(value, technicalIdentifiers) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		aliases = append(aliases, value)
	}
	return aliases
}

func personDisplayNameIsHumanReadable(value string, technicalIdentifiers []string) bool {
	value = strings.Join(strings.Fields(value), " ")
	valueIsOneASCIICharacter := len(value) == 1
	if value == "" || valueIsOneASCIICharacter || personNameIsHandle(value) || hashLikePersonName(value) {
		return false
	}
	normalizedValue := strings.ToLower(value)
	for _, technicalIdentifier := range technicalIdentifiers {
		normalizedTechnicalIdentifier := strings.ToLower(strings.TrimSpace(technicalIdentifier))
		if normalizedValue == normalizedTechnicalIdentifier {
			return false
		}
		if _, identifierWithoutService, hasService := strings.Cut(normalizedTechnicalIdentifier, ":"); hasService && normalizedValue == identifierWithoutService {
			return false
		}
	}
	return true
}

func personMachineIdentifiers(person model.Person) []string {
	identifiers := []string{
		person.ID,
		person.Apple.ID,
		person.Apple.Resource,
		person.Google.ID,
		person.Google.Resource,
	}
	appendAccountIdentifiers := func(accounts map[string][]string) {
		for _, accountIdentifiers := range accounts {
			identifiers = append(identifiers, accountIdentifiers...)
		}
	}
	appendAccountIdentifiers(person.Accounts)
	for _, source := range person.Sources {
		appendAccountIdentifiers(source.Accounts)
	}
	return identifiers
}

func personNameIsHandle(value string) bool {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "@") || strings.HasPrefix(value, "+") {
		return true
	}
	for _, firstCharacter := range value {
		return !unicode.IsLetter(firstCharacter)
	}
	return true
}

func hashLikePersonName(value string) bool {
	compact := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if len(compact) < 16 {
		return false
	}
	for _, character := range compact {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func personCommandResponse(person model.Person) *commandv1.TrawlerCommandResponse {
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_PersonRecord{
			PersonRecord: personRecord(person),
		},
	}
}

func personAnnotationCommandResponse(person model.Person) *commandv1.TrawlerCommandResponse {
	personDisplayName := personHumanName(person)
	if personDisplayName == "" {
		personDisplayName = "Contact"
	}
	fields := []*presentationv1.TrawlerSpecificCommandDetailPresentationField{}
	fields = appendNonEmptyDetailText(fields, "Person", personDisplayName)
	fields = append(fields, detailCanonicalRecordReference("Link", archive.PersonRef(person.ID)))
	fields = appendNonEmptyDetailText(fields, "Annotation", person.Annotation)
	fields = appendNonEmptyDetailText(fields, "Stated", person.AnnotationStatedAt)
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &commandv1.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
					TrawlerSpecificCommandDetailPresentation: &presentationv1.TrawlerSpecificCommandDetailPresentation{
						DetailDisplayName:    "Person annotation recorded",
						FieldsInDisplayOrder: fields,
					},
				},
			},
		},
	}
}

func appendNonEmptyDetailText(
	fields []*presentationv1.TrawlerSpecificCommandDetailPresentationField,
	displayName string,
	displayValue string,
) []*presentationv1.TrawlerSpecificCommandDetailPresentationField {
	displayValue = strings.TrimSpace(displayValue)
	if displayValue == "" {
		return fields
	}
	return append(fields, &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: displayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_Text{Text: displayValue},
		},
	})
}

func detailCanonicalRecordReference(displayName string, canonicalRecordReference string) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: displayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment{
				CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment: canonicalRecordReference,
			},
		},
	}
}

func postalAddressForDisplay(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(value, "\n", ", ")), " ")
}

func sortedSourceNames(person model.Person) []string {
	names := make([]string, 0, len(person.Sources))
	for sourceName := range person.Sources {
		if displayName := personSourceTrawlerDisplayName(sourceName); displayName != "" {
			names = append(names, displayName)
		}
	}
	sort.Strings(names)
	return names
}

func personSourceTrawlerDisplayName(sourceName string) string {
	sourceName = strings.TrimSpace(sourceName)
	switch sourceName {
	case "apple":
		return "Contacts"
	case "imessage":
		return "iMessage"
	case "telegram":
		return "Telegram"
	case "whatsapp":
		return "WhatsApp"
	case "calendar":
		return "Calendar"
	default:
		return sourceName
	}
}
