package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/mail/internal/applemailsource"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type messageParticipant struct {
	role         string
	emailAddress mailparse.EmailAddress
}

func insertMessageParticipants(ctx context.Context, tx *sql.Tx, identifier ArchivedMailMessageIdentifier, parsed mailparse.ParsedEmailMessage) error {
	participants := []messageParticipant{{role: "from", emailAddress: parsed.SenderEmailAddress}}
	for _, address := range parsed.RecipientEmailAddresses {
		participants = append(participants, messageParticipant{role: "to", emailAddress: address})
	}
	for _, address := range parsed.CopiedRecipientEmailAddresses {
		participants = append(participants, messageParticipant{role: "cc", emailAddress: address})
	}
	for _, participant := range participants {
		key := participantKey(participant.emailAddress)
		if key == "" {
			continue
		}
		displayName := strings.TrimSpace(participant.emailAddress.DisplayName)
		if displayName == "" {
			displayName = string(participant.emailAddress.NormalizedEmailAddress)
		}
		if _, err := tx.ExecContext(ctx, `
insert into message_participants(message_id, role, name, address, display_name, participant_key)
values (?, ?, ?, ?, ?, ?)
on conflict(message_id, role, participant_key, name, address) do nothing`,
			identifier, participant.role, participant.emailAddress.DisplayName, string(participant.emailAddress.NormalizedEmailAddress), displayName, key,
		); err != nil {
			return fmt.Errorf("insert message participant: %w", err)
		}
	}
	return nil
}

// participantKey identifies a person by email address, or by name when the
// header carried no address.
func participantKey(address mailparse.EmailAddress) string {
	if address.NormalizedEmailAddress != "" {
		return "addr:" + string(address.NormalizedEmailAddress)
	}
	if name := whomatch.Normalize(address.DisplayName); name != "" {
		return "name:" + name
	}
	return ""
}

// OwnerEmailAddresses are the addresses the archive owner sends from: every
// sender address in a Sent mailbox, and each account's user name.
func (s *Store) OwnerEmailAddresses(ctx context.Context) (map[mailparse.NormalizedEmailAddress]struct{}, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select distinct apple_mail_mailbox_name, from_address from messages where from_address <> ''
union
select '', apple_mail_account_user_name from apple_mail_accounts where apple_mail_account_user_name like '%@%'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	owners := map[mailparse.NormalizedEmailAddress]struct{}{}
	for rows.Next() {
		var mailboxName, address string
		if err := rows.Scan(&mailboxName, &address); err != nil {
			return nil, err
		}
		if mailboxName == "" || (applemailsource.AppleMailMailbox{AppleMailMailboxName: mailboxName}).IsSentMailbox() {
			owners[mailparse.NormalizeEmailAddress(address)] = struct{}{}
		}
	}
	return owners, rows.Err()
}
