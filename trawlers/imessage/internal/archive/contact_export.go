package archive

import (
	"context"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

type contactHandle struct {
	ID          string
	DisplayName string
	Messages    int64
	LastMessage int64
}

func (s *Store) ExportContacts(ctx context.Context) ([]control.Contact, error) {
	if s.schemaOutdated {
		return nil, ErrSchemaOutdated
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select
  h.handle,
  coalesce(h.display_name, ''),
  count(m.source_rowid) as messages,
  coalesce(max(m.date), 0) as last_message
from handles h
left join messages m on m.handle_rowid = h.source_rowid
group by h.source_rowid, h.handle, h.display_name
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byIdentifier := map[string]contactHandle{}
	order := make([]string, 0)
	for rows.Next() {
		var row contactHandle
		if err := rows.Scan(&row.ID, &row.DisplayName, &row.Messages, &row.LastMessage); err != nil {
			return nil, err
		}
		key := contactIdentifierKey(row.ID)
		if key == "" {
			continue
		}
		if current, ok := byIdentifier[key]; ok {
			if preferContactHandle(row, current) {
				byIdentifier[key] = row
			}
			continue
		}
		byIdentifier[key] = row
		order = append(order, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(order, func(i, j int) bool {
		left := byIdentifier[order[i]]
		right := byIdentifier[order[j]]
		if left.LastMessage != right.LastMessage {
			return left.LastMessage > right.LastMessage
		}
		return order[i] < order[j]
	})
	out := make([]control.Contact, 0, len(order))
	for _, key := range order {
		row := byIdentifier[key]
		handle := strings.TrimSpace(row.ID)
		name := strings.TrimSpace(row.DisplayName)
		if name == "" {
			if !messages.LooksPhoneLike(handle) && !strings.Contains(handle, "@") {
				continue
			}
			name = handle
		}
		if name == "" {
			continue
		}
		contact := control.Contact{DisplayName: name}
		switch {
		case messages.LooksPhoneLike(handle):
			contact.PhoneNumbers = []string{handle}
		case strings.Contains(handle, "@"):
			contact.EmailAddresses = []string{strings.ToLower(handle)}
		default:
			contact.Accounts = map[string][]string{"imessage": {handle}}
		}
		out = append(out, contact)
	}
	return out, nil
}

func contactIdentifierKey(handle string) string {
	handle = strings.TrimSpace(handle)
	switch {
	case handle == "":
		return ""
	case messages.LooksPhoneLike(handle):
		return "phone:" + messages.NormalizePhone(handle)
	case strings.Contains(handle, "@"):
		return "email:" + strings.ToLower(handle)
	default:
		return "imessage:" + strings.ToLower(handle)
	}
}

func preferContactHandle(candidate, current contactHandle) bool {
	if candidate.LastMessage != current.LastMessage {
		return candidate.LastMessage > current.LastMessage
	}
	if candidate.Messages != current.Messages {
		return candidate.Messages > current.Messages
	}
	if candidate.DisplayName != "" && current.DisplayName == "" {
		return true
	}
	return len([]rune(candidate.DisplayName)) > len([]rune(current.DisplayName))
}
