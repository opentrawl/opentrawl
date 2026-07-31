package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) runContacts(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	r := c.handler(ctx, req)
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, usageErr(errors.New("contacts takes flags only"))
	}
	n, err := flags.Limit(c.contacts.Limit, c.contacts.LimitSet)
	if err != nil {
		return nil, usageErr(err)
	}
	var response *command.TrawlerCommandResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		contacts, err := st.ListContacts(r.ctx, n)
		if err != nil {
			return err
		}
		total, err := st.CountContacts(r.ctx)
		if err != nil {
			return err
		}
		personRecords := make([]*person.PersonRecord, 0, len(contacts))
		for _, contact := range contacts {
			personRecords = append(
				personRecords,
				&person.PersonRecord{
					PersonDisplayName: contactDisplayName(contact),
				},
			)
		}
		response = &command.TrawlerCommandResponse{
			TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PersonListResponse{
				PersonListResponse: &person.PersonListResponse{
					PersonRecordsInDisplayOrder: personRecords,
					TotalMatchingPersonCount:    uint64(total),
					MoreMatchingPeopleExist:     total > len(contacts),
				},
			},
		}
		return nil
	})
	return response, err
}

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
			PersonIdentifierWithinTrawlerArchive:        trawlerOwnedPersonIdentifier,
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
				PersonAccountServiceName: "telegram",
				PersonAccountIdentifiers: telegramAccountIdentifiers,
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

func contactDisplayName(contact store.Contact) string {
	return store.ContactDisplayName(contact)
}
