package contacts

import (
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type personListResponseValues struct {
	peopleInDisplayOrder     []model.Person
	totalMatchingPersonCount int
	moreMatchingPeopleExist  bool
}

func personListCommandResponse(
	personListValues personListResponseValues,
) (*command.TrawlerCommandResponse, error) {
	personRecords := make(
		[]*person.PersonRecord,
		0,
		len(personListValues.peopleInDisplayOrder),
	)
	for _, contactPerson := range personListValues.peopleInDisplayOrder {
		personRecords = append(personRecords, personRecord(contactPerson))
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PersonListResponse{
			PersonListResponse: &person.PersonListResponse{
				PersonRecordsInDisplayOrder: personRecords,
				TotalMatchingPersonCount:    uint64(personListValues.totalMatchingPersonCount),
				MoreMatchingPeopleExist:     personListValues.moreMatchingPeopleExist,
			},
		},
	}, nil
}

func personRecord(contactPerson model.Person) *person.PersonRecord {
	personDisplayName := personHumanName(contactPerson)
	if personDisplayName == "" {
		personDisplayName = "Contact"
	}
	return &person.PersonRecord{
		CanonicalRecordReference:                  trawlkit.NewCanonicalArchiveRecordReference(archive.PersonRef(contactPerson.ID)),
		PersonDisplayName:                         personDisplayName,
		AlternativePersonDisplayNames:             personKnownAs(contactPerson, personDisplayName),
		PersonContactMethodsInDisplayOrder:        personContactMethods(contactPerson),
		PersonFactContributingTrawlerDisplayNames: sortedSourceNames(contactPerson),
		PersonMessageCountsFromTrawlerArchives:    personMessageCountsForHumanOutput(contactPerson),
		MessageCountInvolvingPersonAcrossTrawlers: messageCountInvolvingPersonAcrossTrawlers(contactPerson),
	}
}

func personMessageCountsForHumanOutput(contactPerson model.Person) []*person.PersonMessageCountFromTrawlerArchive {
	messageCounts := make([]*person.PersonMessageCountFromTrawlerArchive, 0, len(contactPerson.Sources))
	for registeredTrawlerIdentity, source := range contactPerson.Sources {
		if source.MessageCountInvolvingPersonInSourceArchive == 0 {
			continue
		}
		messageCounts = append(messageCounts, &person.PersonMessageCountFromTrawlerArchive{
			RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity(registeredTrawlerIdentity),
			RegisteredTrawlerDisplayName: personSourceTrawlerDisplayName(
				registeredTrawlerIdentity,
			),
			MessageCountInvolvingPersonInTrawlerArchive: source.MessageCountInvolvingPersonInSourceArchive,
		})
	}
	sort.SliceStable(messageCounts, func(left, right int) bool {
		leftCount := messageCounts[left].GetMessageCountInvolvingPersonInTrawlerArchive()
		rightCount := messageCounts[right].GetMessageCountInvolvingPersonInTrawlerArchive()
		if leftCount != rightCount {
			return leftCount > rightCount
		}
		return strings.ToLower(messageCounts[left].GetRegisteredTrawlerDisplayName()) <
			strings.ToLower(messageCounts[right].GetRegisteredTrawlerDisplayName())
	})
	return messageCounts
}

func messageCountInvolvingPersonAcrossTrawlers(contactPerson model.Person) uint64 {
	var messageCount uint64
	for _, source := range contactPerson.Sources {
		messageCount += source.MessageCountInvolvingPersonInSourceArchive
	}
	return messageCount
}

func personContactMethods(contactPerson model.Person) []*person.PersonContactMethod {
	personContactMethods := make(
		[]*person.PersonContactMethod,
		0,
		len(contactPerson.Emails)+len(contactPerson.Phones)+len(contactPerson.Addresses)+len(contactPerson.Accounts),
	)
	for _, emailAddress := range contactPerson.Emails {
		emailAddressDisplayValue := strings.TrimSpace(emailAddress.Value)
		if emailAddressDisplayValue == "" {
			continue
		}
		personContactMethods = append(personContactMethods, &person.PersonContactMethod{
			PersonContactMethodKind:         person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_EMAIL_ADDRESS,
			PersonContactMethodLabel:        humanContactMethodLabel(emailAddress.Label, "email"),
			PersonContactMethodDisplayValue: emailAddressDisplayValue,
		})
	}
	for _, phoneNumber := range contactPerson.Phones {
		if strings.TrimSpace(phoneNumber.Value) == "" {
			continue
		}
		personContactMethods = append(personContactMethods, &person.PersonContactMethod{
			PersonContactMethodKind:         person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_PHONE_NUMBER,
			PersonContactMethodLabel:        humanContactMethodLabel(phoneNumber.Label, "phone"),
			PersonContactMethodDisplayValue: render.FormatPhone(phoneNumber.Value),
		})
	}
	for _, postalAddress := range contactPerson.Addresses {
		postalAddressDisplayValue := postalAddressForDisplay(postalAddress.Value)
		if postalAddressDisplayValue == "" {
			continue
		}
		personContactMethods = append(personContactMethods, &person.PersonContactMethod{
			PersonContactMethodKind:         person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_POSTAL_ADDRESS,
			PersonContactMethodLabel:        humanContactMethodLabel(postalAddress.Label, "address"),
			PersonContactMethodDisplayValue: postalAddressDisplayValue,
		})
	}
	accountServiceNames := make([]string, 0, len(contactPerson.Accounts))
	for accountServiceName := range contactPerson.Accounts {
		accountServiceNames = append(accountServiceNames, accountServiceName)
	}
	sort.Strings(accountServiceNames)
	for _, accountServiceName := range accountServiceNames {
		for _, accountIdentifier := range contactPerson.Accounts[accountServiceName] {
			accountIdentifier = model.AccountIdentifierForHumanPresentation(
				accountServiceName,
				accountIdentifier,
			)
			if accountIdentifier == "" {
				continue
			}
			personContactMethods = append(personContactMethods, &person.PersonContactMethod{
				PersonContactMethodKind:         person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_ACCOUNT_IDENTIFIER,
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
		leftMessageCount := messageCountInvolvingPersonAcrossTrawlers(orderedPeople[left])
		rightMessageCount := messageCountInvolvingPersonAcrossTrawlers(orderedPeople[right])
		if leftMessageCount != rightMessageCount {
			return leftMessageCount > rightMessageCount
		}
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
	return humanReadablePersonDisplayName(person.Name, alternativePersonNames, model.PersonIdentifierValuesNotSuitableAsPersonDisplayNames(person))
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
		model.PersonIdentifierValuesNotSuitableAsPersonDisplayNames(person),
	)
}

func humanReadablePersonDisplayName(primaryName string, alternativeNames, identifierValuesNotSuitableAsPersonDisplayNames []string) string {
	for _, value := range append([]string{primaryName}, alternativeNames...) {
		value = strings.Join(strings.Fields(value), " ")
		if model.PersonDisplayNameIsSuitableForHumanPresentation(value, identifierValuesNotSuitableAsPersonDisplayNames) {
			return value
		}
	}
	return ""
}

func humanReadableAlternativePersonDisplayNames(
	primaryName string,
	alternativeNames []string,
	displayName string,
	identifierValuesNotSuitableAsPersonDisplayNames []string,
) []string {
	aliases := make([]string, 0, len(alternativeNames))
	seen := map[string]struct{}{strings.ToLower(displayName): {}}
	for _, value := range append([]string{primaryName}, alternativeNames...) {
		value = strings.Join(strings.Fields(value), " ")
		key := strings.ToLower(value)
		if !model.PersonDisplayNameIsSuitableForHumanPresentation(value, identifierValuesNotSuitableAsPersonDisplayNames) {
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

func personCommandResponse(person model.Person) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PersonRecord{
			PersonRecord: personRecord(person),
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
