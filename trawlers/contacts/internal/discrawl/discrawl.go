package discrawl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/clawdex/internal/model"
	ckstore "github.com/openclaw/crawlkit/store"
)

type Adapter struct {
	DBPath string
}

type dmRow struct {
	ChannelID     string
	Name          string
	Messages      int
	FirstMessage  string
	LastMessage   string
	CounterpartID string
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
		return nil, fmt.Errorf("discrawl sqlite query: %w", err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.DB().QueryContext(ctx, dmQuery, minMessages)
	if err != nil {
		return nil, fmt.Errorf("discrawl sqlite query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []model.SourceContact
	for rows.Next() {
		var row dmRow
		if err := rows.Scan(&row.ChannelID, &row.Name, &row.Messages, &row.FirstMessage, &row.LastMessage, &row.CounterpartID); err != nil {
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
	name := strings.TrimSpace(r.Name)
	if name == "" || r.ChannelID == "" {
		return model.SourceContact{}, false
	}
	accounts := map[string][]string{"discord": {"channel:" + r.ChannelID}}
	if r.CounterpartID != "" {
		accounts["discord"] = append(accounts["discord"], "user:"+r.CounterpartID)
	}
	return model.SourceContact{
		Source:     "discord",
		ExternalID: r.ChannelID,
		Name:       name,
		Tags:       []string{"discord", "dm"},
		Accounts:   accounts,
	}, true
}

func resolveDBPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".discrawl", "discrawl.db")
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

const dmQuery = `
with dm as (
  select
    c.id as channel_id,
    coalesce(c.name, '') as name,
    count(m.id) as messages,
    coalesce(min(m.created_at), '') as first_message,
    coalesce(max(m.created_at), '') as last_message
  from channels c
  join messages m on m.channel_id = c.id
  where c.guild_id = '@me' and c.kind = 'dm'
  group by c.id, c.name
  having count(m.id) > ?
),
self as (
  select m.author_id
  from channels c
  join messages m on m.channel_id = c.id
  where c.guild_id = '@me' and c.kind = 'dm' and m.author_id is not null
  group by m.author_id
  order by count(distinct c.id) desc, count(m.id) desc
  limit 1
),
author_counts as (
  select c.id as channel_id, m.author_id, count(m.id) as messages
  from channels c
  join messages m on m.channel_id = c.id
  where c.guild_id = '@me' and c.kind = 'dm' and m.author_id is not null
  group by c.id, m.author_id
)
select
  dm.channel_id,
  dm.name,
  dm.messages,
  dm.first_message,
  dm.last_message,
  coalesce((
    select ac.author_id
    from author_counts ac, self
    where ac.channel_id = dm.channel_id and ac.author_id <> self.author_id
    order by ac.messages desc
    limit 1
  ), '') as counterpart_id
from dm
order by dm.messages desc, lower(dm.name);
`
