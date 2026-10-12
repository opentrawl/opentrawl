package render

import (
	"fmt"
	"io"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

func writePersonRecord(
	writer io.Writer,
	personRecord *person.PersonRecord,
	globallyRoutableTrawlLinkForPerson *identity.GloballyRoutableTrawlLink,
) error {
	if personRecord == nil {
		return fmt.Errorf("person record is missing")
	}
	fields := make([]cardField, 0, 3+len(personRecord.GetPersonContactMethodsInDisplayOrder()))
	if alternativePersonDisplayNames := strings.TrimSpace(strings.Join(
		personRecord.GetAlternativePersonDisplayNames(),
		", ",
	)); alternativePersonDisplayNames != "" {
		fields = append(fields, cardField{label: "Known as", value: alternativePersonDisplayNames})
	}
	if messageCount := personRecord.GetMessageCountInvolvingPersonAcrossTrawlers(); messageCount > 0 {
		fields = append(fields, cardField{label: "Messages", value: FormatInteger(int64(messageCount))})
	}
	personApps := personAppsFromContributingTrawlersAndMessageCounts(
		personRecord.GetTrawlersContributingFactsToPersonRecord(),
		personRecord.GetPersonMessageCountsFromTrawlerArchives(),
	)
	if appDisplayNames := strings.TrimSpace(strings.Join(
		personAppDisplayNamesInAlphabeticalOrder(personApps), ", ",
	)); appDisplayNames != "" {
		fields = append(fields, cardField{label: "Apps", value: appDisplayNames})
	}
	for _, app := range personAppsInActivityOrder([]personListRowFacts{{
		apps: personApps,
	}}) {
		messageCount := personMessageCountForApp(personApps, app.registeredTrawler)
		if messageCount == 0 {
			continue
		}
		fields = append(fields, cardField{
			label: "Messages in " + app.appDisplayName,
			value: FormatInteger(int64(messageCount)),
		})
	}
	if annotation := personRecord.GetPersonRelationshipOrContextAnnotation(); annotation != nil {
		if description := strings.TrimSpace(
			annotation.GetPersonRelationshipOrContextDescription(),
		); description != "" {
			fields = append(fields, cardField{label: "Relationship or context", value: description})
		}
		if statedDate := annotation.GetPersonRelationshipOrContextDescriptionStatedDate(); statedDate != nil {
			fields = append(fields, cardField{
				label: "Stated",
				value: formatCalendarDate(statedDate),
			})
		}
	}
	for _, personContactMethod := range personRecord.GetPersonContactMethodsInDisplayOrder() {
		personContactMethodFieldLabel, personContactMethodDisplayValue, err :=
			personContactMethodField(personContactMethod)
		if err != nil {
			return err
		}
		fields = append(fields, cardField{
			label: personContactMethodFieldLabel,
			value: personContactMethodDisplayValue,
		})
	}
	// An opened record does not repeat the link the reader typed; the next
	// command carries it.
	globallyRoutableTrawlLinkForPersonHumanOutput := globallyRoutableTrawlLinkText(globallyRoutableTrawlLinkForPerson)
	return writeCard(writer, card{
		title:        strings.TrimSpace(personRecord.GetPersonDisplayName()),
		fields:       fields,
		nextCommands: []NextCommand{nextCommandListingConversationsWithPerson(globallyRoutableTrawlLinkForPersonHumanOutput)},
	})
}

func personContactMethodField(
	personContactMethod *person.PersonContactMethod,
) (string, string, error) {
	if personContactMethod == nil {
		return "", "", fmt.Errorf("person contact method is missing")
	}
	personContactMethodKindDisplayName := ""
	switch personContactMethod.GetPersonContactMethodKind() {
	case person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_EMAIL_ADDRESS:
		personContactMethodKindDisplayName = "email"
	case person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_PHONE_NUMBER:
		personContactMethodKindDisplayName = "phone"
	case person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_POSTAL_ADDRESS:
		personContactMethodKindDisplayName = "address"
	case person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_ACCOUNT_IDENTIFIER:
		personContactMethodKindDisplayName = "account"
	default:
		return "", "", fmt.Errorf("person contact method kind is unspecified")
	}
	personContactMethodLabel := strings.TrimSpace(personContactMethod.GetPersonContactMethodLabel())
	if personContactMethodLabel == "" || strings.EqualFold(personContactMethodLabel, personContactMethodKindDisplayName) {
		personContactMethodLabel = personContactMethodKindDisplayName
	} else {
		personContactMethodLabel += " " + personContactMethodKindDisplayName
	}
	personContactMethodDisplayValue := strings.TrimSpace(
		personContactMethod.GetPersonContactMethodDisplayValue(),
	)
	if personContactMethodDisplayValue == "" {
		return "", "", fmt.Errorf("%s person contact method display value is empty", personContactMethodKindDisplayName)
	}
	return personContactMethodLabel, personContactMethodDisplayValue, nil
}

// WritePersonMatches writes the people who match a name: one person as a
// card, several as rows.
func (output TrawlCommandOutput) WritePersonMatches(
	operation *federation.FederatedTrawlerPersonMatchOperation,
	nextCommandShowingMorePeople []NextCommand,
) error {
	if operation == nil {
		return fmt.Errorf("federated person match operation is missing")
	}
	writer := output.standardOutput
	personMatchCandidates := nonNilPersonMatchCandidates(operation.GetPersonMatchCandidates())
	if err := writePersonMatchCandidates(writer, personMatchCandidates); err != nil {
		return err
	}
	if len(personMatchCandidates) > 1 {
		nextCommands := append([]NextCommand{nextCommandListingConversationsWithPerson(strings.TrimSpace(
			personMatchCandidates[0].GetPersonTrawlLink().GetGloballyRoutableTrawlLink(),
		))}, nextCommandShowingMorePeople...)
		if err := writeNextCommands(writer, nextCommands); err != nil {
			return err
		}
	} else if err := writeNextCommands(writer, nextCommandShowingMorePeople); err != nil {
		return err
	}
	return output.WriteTrawlerOperationFailures(
		operation.GetOperationFailures(),
		operation.GetTrawlersSkippedFromOperation(),
		false,
	)
}

// writePossiblyMatchingPeople lists the people a failed name could mean.
func writePossiblyMatchingPeople(
	writer io.Writer,
	personMatchCandidates []*federation.FederatedPersonMatchCandidate,
) error {
	if err := writeLine(writer, "Possible matches:"); err != nil {
		return err
	}
	return writePersonMatchCandidateRows(writer, personMatchCandidates)
}

func writePersonMatchCandidateRows(
	writer io.Writer,
	personMatchCandidates []*federation.FederatedPersonMatchCandidate,
) error {
	personMatchCandidates = nonNilPersonMatchCandidates(personMatchCandidates)
	rows := make([]personListRowFacts, 0, len(personMatchCandidates))
	for _, personMatchCandidate := range personMatchCandidates {
		apps := personMatchAppFacts(personMatchCandidate)
		rows = append(rows, personListRowFacts{
			personDisplayName:             strings.TrimSpace(personMatchCandidate.GetPersonDisplayName()),
			alternativePersonDisplayNames: personMatchMatchedAs(personMatchCandidate),
			totalMessageCount:             personMatchMessageCount(personMatchCandidate),
			apps:                          apps,
			globallyRoutableTrawlLink: strings.TrimSpace(
				personMatchCandidate.GetPersonTrawlLink().GetGloballyRoutableTrawlLink(),
			),
		})
	}
	columns, tableRows := personListColumnsAndRowsForOutputWidth(
		rows,
		"matched as",
		readableTableOutputWidth(writer),
	)
	if err := writeHumanRecordRowsWithPrimaryContentColumn(writer, columns, tableRows, 0); err != nil {
		return err
	}
	return nil
}

func writePersonMatchCandidates(
	writer io.Writer,
	candidates []*federation.FederatedPersonMatchCandidate,
) error {
	candidates = nonNilPersonMatchCandidates(candidates)
	switch len(candidates) {
	case 0:
		return nil
	case 1:
		candidate := candidates[0]
		var fields []cardField
		knownAs := alternativePersonDisplayNames(candidate)
		matchedAs := personMatchMatchedAs(candidate)
		if knownAs != "" && !strings.EqualFold(knownAs, matchedAs) {
			fields = append(fields, cardField{label: "Known as", value: knownAs})
		}
		if matchedAs != "" {
			fields = append(fields, cardField{label: "Matched as", value: matchedAs})
		}
		if messageCount := personMatchMessageCount(candidate); messageCount != "" {
			fields = append(fields, cardField{label: "Messages", value: messageCount})
		}
		apps := personMatchAppFacts(candidate)
		if len(apps) > 0 {
			fields = append(fields, cardField{
				label: "Apps",
				value: strings.Join(personAppDisplayNamesInAlphabeticalOrder(apps), ", "),
			})
		}
		for _, app := range personAppsInActivityOrder([]personListRowFacts{{
			apps: apps,
		}}) {
			messageCount := personMessageCountForApp(apps, app.registeredTrawler)
			if messageCount == 0 {
				continue
			}
			fields = append(fields, cardField{
				label: "Messages in " + app.appDisplayName,
				value: FormatInteger(int64(messageCount)),
			})
		}
		globallyRoutableTrawlLinkForPerson := strings.TrimSpace(
			candidate.GetPersonTrawlLink().GetGloballyRoutableTrawlLink(),
		)
		if globallyRoutableTrawlLinkForPerson != "" {
			fields = append(fields, cardField{label: "Link", value: globallyRoutableTrawlLinkForPerson})
		}
		return writeCard(writer, card{
			title:        strings.TrimSpace(candidate.GetPersonDisplayName()),
			fields:       fields,
			nextCommands: []NextCommand{nextCommandListingConversationsWithPerson(globallyRoutableTrawlLinkForPerson)},
		})
	default:
		return writePersonMatchCandidateRows(writer, candidates)
	}
}

func personMatchMessageCount(candidate *federation.FederatedPersonMatchCandidate) string {
	if candidate.GetMessageCountInvolvingPersonAcrossTrawlers() == 0 {
		return ""
	}
	return FormatInteger(int64(candidate.GetMessageCountInvolvingPersonAcrossTrawlers()))
}

func nonNilPersonMatchCandidates(
	candidates []*federation.FederatedPersonMatchCandidate,
) []*federation.FederatedPersonMatchCandidate {
	kept := make([]*federation.FederatedPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate != nil {
			kept = append(kept, candidate)
		}
	}
	return kept
}

func alternativePersonDisplayNames(candidate *federation.FederatedPersonMatchCandidate) string {
	primary := strings.TrimSpace(candidate.GetPersonDisplayName())
	seen := map[string]struct{}{strings.ToLower(primary): {}}
	names := make([]string, 0, len(candidate.GetAlternativePersonDisplayNames()))
	for _, name := range candidate.GetAlternativePersonDisplayNames() {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func personMatchMatchedAs(candidate *federation.FederatedPersonMatchCandidate) string {
	if candidate == nil {
		return ""
	}
	matchedAs := strings.TrimSpace(
		candidate.GetPersonNameOrHumanReadableContactValueThatMatchedQuery(),
	)
	if matchedAs == "" || strings.EqualFold(matchedAs, strings.TrimSpace(candidate.GetPersonDisplayName())) {
		return ""
	}
	return matchedAs
}

func personMatchAppFacts(
	candidate *federation.FederatedPersonMatchCandidate,
) []personAppFacts {
	apps := make([]personAppFacts, 0, len(candidate.GetPersonMatchFactsFromTrawlers()))
	for _, facts := range candidate.GetPersonMatchFactsFromTrawlers() {
		if facts == nil {
			continue
		}
		apps = mergePersonAppFactsByRegisteredTrawlerIdentity(apps, personAppFacts{
			registeredTrawler: facts.GetRegisteredTrawler(),
			appDisplayName:    strings.TrimSpace(facts.GetRegisteredTrawlerDisplayName()),
		})
	}
	for _, messageCount := range candidate.GetPersonMessageCountsFromTrawlerArchives() {
		if messageCount == nil {
			continue
		}
		apps = mergePersonAppFactsByRegisteredTrawlerIdentity(apps, personAppFacts{
			registeredTrawler: messageCount.GetRegisteredTrawler(),
			appDisplayName:    strings.TrimSpace(messageCount.GetRegisteredTrawlerDisplayName()),
			messageCount:      messageCount.GetMessageCountInvolvingPersonInTrawlerArchive(),
		})
	}
	return apps
}
