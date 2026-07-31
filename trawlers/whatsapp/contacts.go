package whatsapp

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*person.TrawlerPeopleSnapshot, error) {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(err)
	}
	peopleWithMessageActivity, err := st.PersonIdentitiesWithMessageActivityForPeopleSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &person.TrawlerPeopleSnapshot{
		TrawlerPersonIdentities: exportPeopleWithMessageActivity(peopleWithMessageActivity),
	}, nil
}

func exportPeopleWithMessageActivity(
	peopleWithMessageActivity []store.WhoCandidate,
) []*person.TrawlerPersonIdentity {
	identities := make([]*person.TrawlerPersonIdentity, 0, len(peopleWithMessageActivity))
	for _, personWithMessageActivity := range peopleWithMessageActivity {
		trawlerOwnedPersonIdentifier := stableWhatsAppPersonIdentifier(personWithMessageActivity.ParticipantKeys)
		personDisplayName := humanParticipantLabel(outputField(personWithMessageActivity.Who))
		if trawlerOwnedPersonIdentifier == "" || personDisplayName == "" || personDisplayName == "me" {
			continue
		}
		personIdentity := &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive:        trawlerOwnedPersonIdentifier,
			PersonDisplayName:                           personDisplayName,
			MessageCountInvolvingPersonInTrawlerArchive: uint64(personWithMessageActivity.Messages),
		}
		if phoneNumber := whatsappPhoneNumberFromPersonIdentifier(
			trawlerOwnedPersonIdentifier,
		); phoneNumber != "" {
			personIdentity.PersonPhoneNumbers = append(
				personIdentity.PersonPhoneNumbers,
				phoneNumber,
			)
		}
		for _, identifier := range personWithMessageActivity.Identifiers {
			identifier = strings.TrimSpace(identifier)
			if identifier == "" || strings.EqualFold(identifier, "me") {
				continue
			}
			if looksLikePhone(identifier) {
				personIdentity.PersonPhoneNumbers = append(personIdentity.PersonPhoneNumbers, identifier)
				continue
			}
			if personIdentity.PersonAccountIdentifiersByServiceName == nil {
				personIdentity.PersonAccountIdentifiersByServiceName =
					map[string]*person.TrawlerPersonAccountIdentifiers{}
			}
			personIdentity.PersonAccountIdentifiersByServiceName["whatsapp"] =
				&person.TrawlerPersonAccountIdentifiers{
					PersonAccountIdentifiers: append(
						personIdentity.PersonAccountIdentifiersByServiceName["whatsapp"].GetPersonAccountIdentifiers(),
						identifier,
					),
				}
		}
		if !personWithMessageActivity.LastSeen.IsZero() {
			personIdentity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive =
				timestamppb.New(personWithMessageActivity.LastSeen)
		}
		identities = append(identities, personIdentity)
	}
	return identities
}

func stableWhatsAppPersonIdentifier(participantKeys []string) string {
	for _, participantKey := range participantKeys {
		participantKey = strings.TrimSpace(participantKey)
		if strings.HasPrefix(participantKey, "jid:") {
			return participantKey
		}
	}
	return ""
}

func whatsappPhoneNumberFromPersonIdentifier(personIdentifierWithinWhatsAppArchive string) string {
	whatsAppAccountIdentifier := strings.TrimPrefix(
		strings.TrimSpace(personIdentifierWithinWhatsAppArchive),
		"jid:",
	)
	for _, individualWhatsAppAccountSuffix := range []string{
		"@s.whatsapp.net",
		"@c.us",
	} {
		if !strings.HasSuffix(
			strings.ToLower(whatsAppAccountIdentifier),
			individualWhatsAppAccountSuffix,
		) {
			continue
		}
		phoneNumber := whatsAppAccountIdentifier[:len(whatsAppAccountIdentifier)-len(individualWhatsAppAccountSuffix)]
		if deviceIdentifierSeparatorIndex := strings.Index(phoneNumber, ":"); deviceIdentifierSeparatorIndex >= 0 {
			phoneNumber = phoneNumber[:deviceIdentifierSeparatorIndex]
		}
		if looksLikePhone(phoneNumber) {
			return phoneNumber
		}
	}
	return ""
}
