package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*person.TrawlerPeopleSnapshot, error) {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	peopleWithMessageActivity, err := st.PersonIdentitiesWithMessageActivityForPeopleSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*person.TrawlerPersonIdentity, 0, len(peopleWithMessageActivity))
	for _, personWithMessageActivity := range peopleWithMessageActivity {
		trawlerOwnedPersonIdentifier := stableTelegramPersonIdentifier(personWithMessageActivity)
		personDisplayName := humanTelegramPersonDisplayName(personWithMessageActivity)
		if trawlerOwnedPersonIdentifier == "" || personDisplayName == "" {
			continue
		}
		personIdentity := &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive:        trawlkit.NewPersonIdentifierWithinTrawlerArchive(trawlerOwnedPersonIdentifier),
			PersonDisplayName:                           personDisplayName,
			MessageCountInvolvingPersonInTrawlerArchive: uint64(personWithMessageActivity.Messages),
		}
		telegramAccountIdentifiers := []string{trawlerOwnedPersonIdentifier}
		for _, identifier := range personWithMessageActivity.Identifiers {
			identifier = strings.TrimSpace(identifier)
			switch {
			case identifier == "":
			case telegramIdentifierIsPhoneNumber(identifier, trawlerOwnedPersonIdentifier):
				personIdentity.PersonPhoneNumbers = appendUniqueTelegramPersonIdentifier(
					personIdentity.PersonPhoneNumbers,
					identifier,
				)
			default:
				telegramAccountIdentifiers = appendUniqueTelegramPersonIdentifier(
					telegramAccountIdentifiers,
					identifier,
				)
			}
		}
		personIdentity.PersonAccountIdentifiersForServices =
			[]*person.TrawlerPersonAccountIdentifiersForService{{
				PersonAccountServiceName:              "telegram",
				PersonAccountIdentifiersWithinService: trawlkit.NewPersonAccountIdentifiersWithinService(telegramAccountIdentifiers),
			}}
		if !personWithMessageActivity.LastSeen.IsZero() {
			personIdentity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive =
				timestamppb.New(personWithMessageActivity.LastSeen)
		}
		out = append(out, personIdentity)
	}
	return &person.TrawlerPeopleSnapshot{TrawlerPersonIdentities: out}, nil
}

func stableTelegramPersonIdentifier(personWithMessageActivity store.WhoCandidate) string {
	for _, participant := range personWithMessageActivity.Participants {
		if participantJID := strings.TrimSpace(participant.JID); participantJID != "" {
			return participantJID
		}
	}
	return ""
}

func humanTelegramPersonDisplayName(personWithMessageActivity store.WhoCandidate) string {
	personDisplayName := strings.Join(strings.Fields(personWithMessageActivity.Who), " ")
	if whomatch.IsIdentifierLike(personDisplayName, personWithMessageActivity.Identifiers) {
		return ""
	}
	return personDisplayName
}

func telegramIdentifierIsPhoneNumber(identifier, trawlerOwnedPersonIdentifier string) bool {
	identifier = strings.TrimSpace(identifier)
	if strings.EqualFold(identifier, trawlerOwnedPersonIdentifier) {
		return false
	}
	phoneNumberWithoutInternationalDiallingPrefix := strings.TrimPrefix(identifier, "+")
	if len(phoneNumberWithoutInternationalDiallingPrefix) < 5 {
		return false
	}
	for _, character := range phoneNumberWithoutInternationalDiallingPrefix {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func appendUniqueTelegramPersonIdentifier(identifiers []string, identifier string) []string {
	for _, existingIdentifier := range identifiers {
		if strings.EqualFold(existingIdentifier, identifier) {
			return identifiers
		}
	}
	return append(identifiers, identifier)
}
