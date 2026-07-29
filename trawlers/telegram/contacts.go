package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) runContacts(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
	r := c.handler(ctx, req)
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, usageErr(errors.New("contacts takes flags only"))
	}
	n, err := flags.Limit(c.contacts.Limit, c.contacts.LimitSet)
	if err != nil {
		return nil, usageErr(err)
	}
	var response *commandv1.TrawlerCommandResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		contacts, err := st.ListContacts(r.ctx, n)
		if err != nil {
			return err
		}
		total, err := st.CountContacts(r.ctx)
		if err != nil {
			return err
		}
		personRecords := make([]*personv1.PersonRecord, 0, len(contacts))
		for _, contact := range contacts {
			personRecords = append(
				personRecords,
				&personv1.PersonRecord{
					PersonDisplayName: contactDisplayName(contact),
				},
			)
		}
		response = &commandv1.TrawlerCommandResponse{
			TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_PersonListResponse{
				PersonListResponse: &personv1.PersonListResponse{
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

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*personv1.TrawlerPeopleSnapshot, error) {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	peopleWithMessageActivity, err := st.PersonIdentitiesWithMessageActivityForPeopleSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*personv1.TrawlerPersonIdentity, 0, len(peopleWithMessageActivity))
	for _, personWithMessageActivity := range peopleWithMessageActivity {
		trawlerOwnedPersonIdentifier := stableTelegramPersonIdentifier(personWithMessageActivity)
		personDisplayName := humanTelegramPersonDisplayName(personWithMessageActivity)
		if trawlerOwnedPersonIdentifier == "" || personDisplayName == "" {
			continue
		}
		personIdentity := &personv1.TrawlerPersonIdentity{
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
				personIdentity.PersonPhoneNumbers = append(personIdentity.PersonPhoneNumbers, identifier)
			default:
				telegramAccountIdentifiers = appendUniqueTelegramPersonIdentifier(
					telegramAccountIdentifiers,
					identifier,
				)
			}
		}
		personIdentity.PersonAccountIdentifiersByServiceName =
			map[string]*personv1.TrawlerPersonAccountIdentifiers{
				"telegram": {PersonAccountIdentifiers: telegramAccountIdentifiers},
			}
		if !personWithMessageActivity.LastSeen.IsZero() {
			personIdentity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive =
				timestamppb.New(personWithMessageActivity.LastSeen)
		}
		out = append(out, personIdentity)
	}
	return &personv1.TrawlerPeopleSnapshot{TrawlerPersonIdentities: out}, nil
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
	if strings.EqualFold(identifier, trawlerOwnedPersonIdentifier) ||
		!strings.HasPrefix(identifier, "+") {
		return false
	}
	for _, character := range strings.TrimPrefix(identifier, "+") {
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
