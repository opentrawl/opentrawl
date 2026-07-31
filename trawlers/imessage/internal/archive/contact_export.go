package archive

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Store) ExportContacts(ctx context.Context) ([]*person.TrawlerPersonIdentity, error) {
	peopleWithMessageActivity, err := s.whoCandidates(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.populateWhoStats(ctx, peopleWithMessageActivity); err != nil {
		return nil, err
	}
	sortWhoCandidates(peopleWithMessageActivity)
	personIdentities := make([]*person.TrawlerPersonIdentity, 0, len(peopleWithMessageActivity))
	for _, personWithMessageActivity := range peopleWithMessageActivity {
		if personWithMessageActivity.includeFromMe {
			continue
		}
		personDisplayName := humanIMessagePersonDisplayName(
			personWithMessageActivity.Who,
			personWithMessageActivity.Identifiers,
		)
		if personDisplayName == "" || personWithMessageActivity.trawlerOwnedPersonIdentifier == "" {
			continue
		}
		personIdentity := &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive:        personWithMessageActivity.trawlerOwnedPersonIdentifier,
			PersonDisplayName:                           personDisplayName,
			MessageCountInvolvingPersonInTrawlerArchive: uint64(personWithMessageActivity.Messages),
		}
		for _, identifier := range personWithMessageActivity.Identifiers {
			identifier = strings.TrimSpace(identifier)
			switch {
			case identifier == "":
			case messages.LooksPhoneLike(identifier):
				personIdentity.PersonPhoneNumbers = append(personIdentity.PersonPhoneNumbers, identifier)
			case strings.Contains(identifier, "@"):
				personIdentity.PersonEmailAddresses = append(
					personIdentity.PersonEmailAddresses,
					strings.ToLower(identifier),
				)
			default:
				if personIdentity.PersonAccountIdentifiersByServiceName == nil {
					personIdentity.PersonAccountIdentifiersByServiceName =
						map[string]*person.TrawlerPersonAccountIdentifiers{}
				}
				personIdentity.PersonAccountIdentifiersByServiceName["imessage"] =
					&person.TrawlerPersonAccountIdentifiers{
						PersonAccountIdentifiers: append(
							personIdentity.PersonAccountIdentifiersByServiceName["imessage"].GetPersonAccountIdentifiers(),
							identifier,
						),
					}
			}
		}
		if personWithMessageActivity.lastSeenRaw > 0 {
			personIdentity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive =
				timestamppb.New(AppleDateTime(personWithMessageActivity.lastSeenRaw).UTC())
		}
		personIdentities = append(personIdentities, personIdentity)
	}
	return personIdentities, nil
}

func humanIMessagePersonDisplayName(personDisplayName string, personIdentifiers []string) string {
	personDisplayName = strings.Join(strings.Fields(personDisplayName), " ")
	if personDisplayName == "" {
		return ""
	}
	for _, personIdentifier := range personIdentifiers {
		if strings.EqualFold(personDisplayName, strings.TrimSpace(personIdentifier)) {
			return ""
		}
	}
	if messages.LooksPhoneLike(personDisplayName) || strings.Contains(personDisplayName, "@") {
		return ""
	}
	return personDisplayName
}
