package render

import (
	"fmt"
	"io"
	"strings"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	identityv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
)

func WritePersonRecord(
	writer io.Writer,
	personRecord *personv1.PersonRecord,
	globallyRoutableTrawlLinkForPerson *identityv1.GloballyRoutableTrawlLink,
) error {
	if personRecord == nil {
		return fmt.Errorf("person record is missing")
	}
	fields := make([]CardField, 0, 3+len(personRecord.GetPersonContactMethodsInDisplayOrder()))
	if alternativePersonDisplayNames := strings.TrimSpace(strings.Join(
		personRecord.GetAlternativePersonDisplayNames(),
		", ",
	)); alternativePersonDisplayNames != "" {
		fields = append(fields, CardField{Label: "Known as", Value: alternativePersonDisplayNames})
	}
	if contributingTrawlerDisplayNames := strings.TrimSpace(strings.Join(
		personRecord.GetPersonFactContributingTrawlerDisplayNames(),
		", ",
	)); contributingTrawlerDisplayNames != "" {
		fields = append(fields, CardField{Label: "Trawlers", Value: contributingTrawlerDisplayNames})
	}
	for _, personContactMethod := range personRecord.GetPersonContactMethodsInDisplayOrder() {
		personContactMethodFieldLabel, personContactMethodDisplayValue, err :=
			personContactMethodField(personContactMethod)
		if err != nil {
			return err
		}
		fields = append(fields, CardField{
			Label: personContactMethodFieldLabel,
			Value: personContactMethodDisplayValue,
		})
	}
	globallyRoutableTrawlLinkForPersonHumanOutput := globallyRoutableTrawlLinkText(globallyRoutableTrawlLinkForPerson)
	if globallyRoutableTrawlLinkForPersonHumanOutput != "" {
		fields = append(fields, CardField{Label: "Link", Value: globallyRoutableTrawlLinkForPersonHumanOutput})
	}
	var hints []string
	if globallyRoutableTrawlLinkForPersonHumanOutput != "" {
		hints = []string{"Conversations: " + trawlCommandLineForDisplay(
			writer,
			[]string{"conversations", "--with", globallyRoutableTrawlLinkForPersonHumanOutput},
		)}
	}
	return WriteCard(writer, Card{
		Title:  strings.TrimSpace(personRecord.GetPersonDisplayName()),
		Fields: fields,
		Hints:  hints,
	})
}

func personContactMethodField(
	personContactMethod *personv1.PersonContactMethod,
) (string, string, error) {
	if personContactMethod == nil {
		return "", "", fmt.Errorf("person contact method is missing")
	}
	personContactMethodKindDisplayName := ""
	switch personContactMethod.GetPersonContactMethodKind() {
	case personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_EMAIL_ADDRESS:
		personContactMethodKindDisplayName = "email"
	case personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_PHONE_NUMBER:
		personContactMethodKindDisplayName = "phone"
	case personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_POSTAL_ADDRESS:
		personContactMethodKindDisplayName = "address"
	case personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_ACCOUNT_IDENTIFIER:
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

func WriteTrawlerPersonMatchResponse(
	writer io.Writer,
	response *personv1.TrawlerPersonMatchResponse,
	registeredTrawlerDisplayName string,
) error {
	if response == nil {
		return fmt.Errorf("person match response is missing")
	}
	candidates := make([]*federationv1.FederatedPersonMatchCandidate, 0, len(response.GetPersonMatchCandidates()))
	for _, candidate := range response.GetPersonMatchCandidates() {
		if candidate == nil {
			continue
		}
		candidates = append(candidates, &federationv1.FederatedPersonMatchCandidate{
			PersonDisplayName:                                     candidate.GetPersonDisplayName(),
			AlternativePersonDisplayNames:                         append([]string(nil), candidate.GetAlternativePersonDisplayNames()...),
			PersonNameOrHumanReadableContactValueThatMatchedQuery: candidate.GetPersonNameOrHumanReadableContactValueThatMatchedQuery(),
			LatestMatchingArchiveRecordTime:                       candidate.GetLatestMatchingArchiveRecordTime(),
			MessageCountInvolvingPersonAcrossTrawlers:             candidate.GetMessageCountInvolvingPerson(),
			PersonTrawlLink:                                       candidate.GetPersonTrawlLink(),
			PersonMatchFactsFromTrawlers: []*personv1.PersonMatchFactsFromTrawler{{
				RegisteredTrawlerDisplayName: strings.TrimSpace(registeredTrawlerDisplayName),
			}},
		})
	}
	return writePersonMatchCandidates(writer, candidates)
}

func WriteFederatedTrawlerPersonMatchOperation(
	writer io.Writer,
	operation *federationv1.FederatedTrawlerPersonMatchOperation,
) error {
	if operation == nil {
		return fmt.Errorf("federated person match operation is missing")
	}
	return writePersonMatchCandidates(writer, operation.GetPersonMatchCandidates())
}

func WriteAmbiguousFederatedTrawlerPersonMatchCandidates(
	writer io.Writer,
	personMatchCandidates []*federationv1.FederatedPersonMatchCandidate,
) error {
	personMatchCandidates = nonNilPersonMatchCandidates(personMatchCandidates)
	rows := make([][]string, 0, len(personMatchCandidates))
	for _, personMatchCandidate := range personMatchCandidates {
		rows = append(rows, []string{
			strings.TrimSpace(personMatchCandidate.GetPersonDisplayName()),
			personMatchMatchedAs(personMatchCandidate),
			personMatchMessageCount(personMatchCandidate),
			personMatchTrawlerNames(personMatchCandidate),
			strings.TrimSpace(personMatchCandidate.GetPersonTrawlLink().GetGloballyRoutableTrawlLink()),
		})
	}
	columns := []TableColumn{
		{Header: "person", Wrap: true, MaximumWrappedLines: 2},
		{Header: "matched as", Wrap: true, MaximumWrappedLines: 2},
		{Header: "messages", AlignRight: true},
		{Header: "trawlers", Wrap: true, MaximumWrappedLines: 2},
		{Header: "link", NeverTruncateCellValues: true},
	}
	if err := WriteTable(writer, columns, rows); err != nil {
		return err
	}
	for _, personMatchCandidate := range personMatchCandidates {
		if strings.TrimSpace(personMatchCandidate.GetPersonTrawlLink().GetGloballyRoutableTrawlLink()) != "" {
			if _, err := fmt.Fprintln(writer); err != nil {
				return err
			}
			if err := WriteTrawlCommandHint(
				writer,
				"Open: "+trawlCommandLineForDisplay(writer, []string{"open", "LINK"}),
			); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func writePersonMatchCandidates(
	writer io.Writer,
	candidates []*federationv1.FederatedPersonMatchCandidate,
) error {
	candidates = nonNilPersonMatchCandidates(candidates)
	switch len(candidates) {
	case 0:
		_, err := fmt.Fprintln(writer, "No person matched.")
		return err
	case 1:
		candidate := candidates[0]
		fields := []CardField{{Label: "Name", Value: strings.TrimSpace(candidate.GetPersonDisplayName())}}
		if knownAs := alternativePersonDisplayNames(candidate); knownAs != "" {
			fields = append(fields, CardField{Label: "Known as", Value: knownAs})
		}
		if matchedAs := personMatchMatchedAs(candidate); matchedAs != "" {
			fields = append(fields, CardField{Label: "Matched as", Value: matchedAs})
		}
		if messageCount := personMatchMessageCount(candidate); messageCount != "" {
			fields = append(fields, CardField{Label: "Messages", Value: messageCount})
		}
		fields = append(fields, CardField{Label: "Trawlers", Value: personMatchTrawlerNames(candidate)})
		globallyRoutableTrawlLinkForPerson := strings.TrimSpace(
			candidate.GetPersonTrawlLink().GetGloballyRoutableTrawlLink(),
		)
		if globallyRoutableTrawlLinkForPerson != "" {
			fields = append(fields, CardField{Label: "Link", Value: globallyRoutableTrawlLinkForPerson})
		}
		hints := []string(nil)
		if globallyRoutableTrawlLinkForPerson != "" {
			hints = append(hints, "Conversations: "+trawlCommandLineForDisplay(
				writer,
				[]string{"conversations", "--with", globallyRoutableTrawlLinkForPerson},
			))
		}
		return WriteCard(writer, Card{
			Fields: fields,
			Hints:  hints,
		})
	default:
		return WriteAmbiguousFederatedTrawlerPersonMatchCandidates(writer, candidates)
	}
}

func personMatchMessageCount(candidate *federationv1.FederatedPersonMatchCandidate) string {
	if candidate.GetMessageCountInvolvingPersonAcrossTrawlers() == 0 {
		return ""
	}
	return FormatInteger(int64(candidate.GetMessageCountInvolvingPersonAcrossTrawlers()))
}

func nonNilPersonMatchCandidates(
	candidates []*federationv1.FederatedPersonMatchCandidate,
) []*federationv1.FederatedPersonMatchCandidate {
	kept := make([]*federationv1.FederatedPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate != nil {
			kept = append(kept, candidate)
		}
	}
	return kept
}

func alternativePersonDisplayNames(candidate *federationv1.FederatedPersonMatchCandidate) string {
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

func personMatchMatchedAs(candidate *federationv1.FederatedPersonMatchCandidate) string {
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

func personMatchTrawlerNames(candidate *federationv1.FederatedPersonMatchCandidate) string {
	names := make([]string, 0, len(candidate.GetPersonMatchFactsFromTrawlers()))
	seen := map[string]struct{}{}
	for _, facts := range candidate.GetPersonMatchFactsFromTrawlers() {
		if facts == nil {
			continue
		}
		name := strings.TrimSpace(facts.GetRegisteredTrawlerDisplayName())
		if name == "" {
			name = strings.TrimSpace(facts.GetRegisteredTrawler().GetRegisteredTrawlerIdentity())
		}
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
