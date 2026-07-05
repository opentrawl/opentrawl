package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/state"
	"github.com/openclaw/crawlkit/whomatch"
)

const (
	derivedEntityType        = "derived"
	participantCountEntityID = "message_participants_message_count"
	ownerEntityType          = "owner"
	accountEmailEntityID     = "account_email"
)

type participant struct {
	Role    string
	Name    string
	Address string
}

func (s *Store) SetOwnerAccount(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if email == "" {
		return nil
	}
	return state.New(s.store.DB()).Set(ctx, sourceName, ownerEntityType, accountEmailEntityID, email)
}

func (s *Store) OwnerEmails(ctx context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if rec, ok, err := state.New(s.store.DB()).Get(ctx, sourceName, ownerEntityType, accountEmailEntityID); err != nil {
		return nil, err
	} else if ok {
		if email := normalizeEmail(rec.Value); email != "" {
			out[email] = struct{}{}
		}
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select distinct from_address
from messages
where labels_json like '%"SENT"%'
  and trim(from_address) <> ''
`)
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

func (s *Store) EnsureParticipants(ctx context.Context) (bool, int64, error) {
	messageCount, err := s.CountMessages(ctx)
	if err != nil {
		return false, 0, err
	}
	rec, ok, err := state.New(s.store.DB()).Get(ctx, sourceName, derivedEntityType, participantCountEntityID)
	if err != nil {
		return false, 0, err
	}
	if ok && strings.TrimSpace(rec.Value) == fmt.Sprintf("%d", messageCount) {
		return false, messageCount, nil
	}
	if err := s.RebuildParticipants(ctx); err != nil {
		return false, 0, err
	}
	return true, messageCount, nil
}

func (s *Store) RebuildParticipants(ctx context.Context) error {
	rows, err := s.store.DB().QueryContext(ctx, `
select id, from_name, from_address, to_address, cc_address
from messages
order by id
`)
	if err != nil {
		return fmt.Errorf("read messages for participants: %w", err)
	}
	type row struct {
		ID          string
		FromName    string
		FromAddress string
		ToAddress   string
		CcAddress   string
	}
	var messages []row
	for rows.Next() {
		var msg row
		if err := rows.Scan(&msg.ID, &msg.FromName, &msg.FromAddress, &msg.ToAddress, &msg.CcAddress); err != nil {
			return err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from message_participants`); err != nil {
			return fmt.Errorf("clear message participants: %w", err)
		}
		for _, msg := range messages {
			value := Message{
				ID:          msg.ID,
				FromName:    msg.FromName,
				FromAddress: msg.FromAddress,
				ToAddress:   msg.ToAddress,
				CcAddress:   msg.CcAddress,
			}
			if err := insertMessageParticipants(ctx, tx, value); err != nil {
				return err
			}
		}
		if err := state.New(tx).Set(ctx, sourceName, derivedEntityType, participantCountEntityID, fmt.Sprintf("%d", len(messages))); err != nil {
			return fmt.Errorf("mark participants rebuilt: %w", err)
		}
		return nil
	})
}

func insertMessageParticipants(ctx context.Context, tx *sql.Tx, msg Message) error {
	for _, p := range messageParticipants(msg) {
		display := participantDisplay(p)
		key := participantKey(p)
		if key == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
insert into message_participants(message_id, role, name, address, display_name, participant_key)
values (?, ?, ?, ?, ?, ?)
on conflict(message_id, role, participant_key, name, address) do nothing
`, msg.ID, p.Role, p.Name, p.Address, display, key); err != nil {
			return fmt.Errorf("insert message participant: %w", err)
		}
	}
	return nil
}

func messageParticipants(msg Message) []participant {
	out := []participant{{
		Role:    "from",
		Name:    strings.TrimSpace(msg.FromName),
		Address: strings.TrimSpace(msg.FromAddress),
	}}
	out = append(out, addressListParticipants("to", msg.ToAddress)...)
	out = append(out, addressListParticipants("cc", msg.CcAddress)...)
	return out
}

func addressListParticipants(role, value string) []participant {
	addresses := parseAddressList(value)
	if len(addresses) == 0 {
		return nil
	}
	out := make([]participant, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, participant{
			Role:    role,
			Name:    strings.TrimSpace(address.Name),
			Address: strings.TrimSpace(address.Address),
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
