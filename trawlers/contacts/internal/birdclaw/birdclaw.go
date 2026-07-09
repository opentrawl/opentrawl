package birdclaw

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

type Adapter struct {
	DBPath string
}

type dmRow struct {
	ConversationID string
	ProfileID      string
	Handle         string
	DisplayName    string
	Title          string
	Messages       int
	FirstMessage   string
	LastMessage    string
}

func (a Adapter) ListDMContacts(ctx context.Context, minMessages int) ([]model.SourceContact, error) {
	if minMessages < 1 {
		minMessages = 1
	}
	dbPath, err := resolveDBPath(a.DBPath)
	if err != nil {
		return nil, err
	}
	st, err := ckstore.OpenForeignReadOnly(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("birdclaw sqlite query: %w", err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.DB().QueryContext(ctx, dmQuery, minMessages)
	if err != nil {
		return nil, fmt.Errorf("birdclaw sqlite query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []model.SourceContact
	for rows.Next() {
		var row dmRow
		if err := rows.Scan(&row.ConversationID, &row.ProfileID, &row.Handle, &row.DisplayName, &row.Title, &row.Messages, &row.FirstMessage, &row.LastMessage); err != nil {
			return nil, err
		}
		contact, ok := row.contact()
		if ok {
			out = append(out, contact)
		}
	}
	return out, rows.Err()
}

func (r dmRow) contact() (model.SourceContact, bool) {
	name := firstNonEmpty(r.DisplayName, r.Title, r.Handle)
	if name == "" || r.ConversationID == "" {
		return model.SourceContact{}, false
	}
	accounts := map[string][]string{"x": {"dm:" + r.ConversationID}}
	if r.Handle != "" {
		accounts["x"] = append(accounts["x"], "@"+strings.TrimPrefix(r.Handle, "@"))
	}
	if r.ProfileID != "" {
		accounts["x"] = append(accounts["x"], "user:"+r.ProfileID)
	}
	return model.SourceContact{
		Source:     "x",
		ExternalID: r.ConversationID,
		Name:       name,
		Tags:       []string{"x", "dm"},
		Accounts:   accounts,
	}, true
}

func resolveDBPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".birdclaw", "birdclaw.sqlite")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

const dmQuery = `
select
  c.id as conversation_id,
  coalesce(c.participant_profile_id, '') as profile_id,
  coalesce(p.handle, '') as handle,
  coalesce(p.display_name, '') as display_name,
  coalesce(c.title, '') as title,
  count(m.id) as messages,
  coalesce(min(m.created_at), '') as first_message,
  coalesce(max(m.created_at), '') as last_message
from dm_conversations c
join dm_messages m on m.conversation_id = c.id
left join profiles p on p.id = c.participant_profile_id
group by c.id, c.participant_profile_id, p.handle, p.display_name, c.title
having count(m.id) > ?
order by messages desc, lower(coalesce(nullif(p.display_name, ''), nullif(c.title, ''), p.handle));
`
