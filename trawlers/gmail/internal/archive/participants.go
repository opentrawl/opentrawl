package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/gmail/internal/gmailapi"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

// The Mail trawler reads this key, read-only, to skip the Gmail account the
// Gmail trawler archives. Keep it exactly ('gmail', 'owner', 'account_email').
const (
	ownerEntityType      = "owner"
	accountEmailEntityID = "account_email"
)

// ErrArchiveBelongsToAnotherGoogleAccount means the archive is bound to a
// different Google account. An archive holds one account for its lifetime.
var ErrArchiveBelongsToAnotherGoogleAccount = errors.New("the Gmail archive belongs to another Google account")

type participant struct {
	Role    string
	Name    string
	Address string
}

func (s *Store) BoundGoogleAccountEmailAddress(ctx context.Context) (mailparse.NormalizedEmailAddress, error) {
	record, found, err := state.New(s.store.DB()).Get(ctx, sourceName, ownerEntityType, accountEmailEntityID)
	if err != nil || !found {
		return "", err
	}
	return mailparse.NormalizeEmailAddress(record.Value), nil
}

// BindGoogleAccountEmailAddress binds an unbound archive to the account, and
// refuses any account other than the one the archive is bound to.
func (s *Store) BindGoogleAccountEmailAddress(ctx context.Context, googleAccountEmailAddress string) error {
	address := mailparse.NormalizeEmailAddress(googleAccountEmailAddress)
	if address == "" {
		return errors.New("google account has no email address")
	}
	bound, err := s.BoundGoogleAccountEmailAddress(ctx)
	if err != nil {
		return err
	}
	if bound == address {
		return nil
	}
	if bound != "" {
		return ErrArchiveBelongsToAnotherGoogleAccount
	}
	return state.New(s.store.DB()).Set(ctx, sourceName, ownerEntityType, accountEmailEntityID, string(address))
}

func (s *Store) OwnerEmails(ctx context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	bound, err := s.BoundGoogleAccountEmailAddress(ctx)
	if err != nil {
		return nil, err
	}
	if bound != "" {
		out[string(bound)] = struct{}{}
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select distinct from_address
from messages
where labels_json like ?
  and trim(from_address) <> ''
  and deleted_from_source_at = ''
`, `%"`+string(gmailapi.GmailSentLabelIdentifier)+`"%`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		if email := normalizeEmail(email); email != "" {
			out[email] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func insertMessageParticipants(ctx context.Context, tx *sql.Tx, identifier gmailapi.GmailMessageIdentifier, parsed mailparse.ParsedEmailMessage) error {
	for _, p := range messageParticipants(parsed) {
		display := participantDisplay(p)
		key := participantKey(p)
		if key == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
insert into message_participants(message_id, role, name, address, display_name, participant_key)
values (?, ?, ?, ?, ?, ?)
on conflict(message_id, role, participant_key, name, address) do nothing
`, identifier, p.Role, p.Name, p.Address, display, key); err != nil {
			return fmt.Errorf("insert message participant: %w", err)
		}
	}
	return nil
}

func messageParticipants(parsed mailparse.ParsedEmailMessage) []participant {
	out := []participant{{
		Role:    "from",
		Name:    strings.TrimSpace(parsed.SenderEmailAddress.DisplayName),
		Address: string(parsed.SenderEmailAddress.NormalizedEmailAddress),
	}}
	out = append(out, addressListParticipants("to", parsed.RecipientEmailAddresses)...)
	out = append(out, addressListParticipants("cc", parsed.CopiedRecipientEmailAddresses)...)
	return out
}

func addressListParticipants(role string, addresses []mailparse.EmailAddress) []participant {
	out := make([]participant, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, participant{
			Role:    role,
			Name:    strings.TrimSpace(address.DisplayName),
			Address: string(address.NormalizedEmailAddress),
		})
	}
	return out
}

func participantDisplay(p participant) string {
	if name := cleanWhoDisplay(p.Name); name != "" {
		return name
	}
	return strings.TrimSpace(p.Address)
}

func participantKey(p participant) string {
	if email := normalizeEmail(p.Address); email != "" {
		return "addr:" + email
	}
	if name := whomatch.Normalize(p.Name); name != "" {
		return "name:" + name
	}
	return ""
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
