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
where h.handle not like '%@%'
group by h.source_rowid, h.handle, h.display_name
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byPhone := map[string]contactHandle{}
	order := make([]string, 0)
	for rows.Next() {
		var row contactHandle
		if err := rows.Scan(&row.ID, &row.DisplayName, &row.Messages, &row.LastMessage); err != nil {
			return nil, err
		}
		phoneKey := messages.NormalizePhone(row.ID)
		if phoneKey == "" || !messages.LooksPhoneLike(row.ID) {
			continue
		}
		if current, ok := byPhone[phoneKey]; ok {
			if preferContactHandle(row, current) {
				byPhone[phoneKey] = row
			}
			continue
		}
		byPhone[phoneKey] = row
		order = append(order, phoneKey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(order, func(i, j int) bool {
		left := byPhone[order[i]]
		right := byPhone[order[j]]
		if left.LastMessage != right.LastMessage {
			return left.LastMessage > right.LastMessage
		}
		return order[i] < order[j]
	})
	out := make([]control.Contact, 0, len(order))
	for _, key := range order {
		row := byPhone[key]
		name := strings.TrimSpace(row.DisplayName)
		if name == "" {
			name = strings.TrimSpace(row.ID)
		}
		if name == "" {
			continue
		}
		out = append(out, control.Contact{DisplayName: name, PhoneNumbers: []string{strings.TrimSpace(row.ID)}})
	}
	return out, nil
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
