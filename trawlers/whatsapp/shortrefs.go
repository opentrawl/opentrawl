package wacrawl

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *trawlkit.Request) ([]trawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select msg_id from messages where trim(msg_id) <> '' order by msg_id`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortRefRecord
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: messageRefPrefix + id})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	// Chat refs join the same index so the chats table can show a short ref and
	// messages --chat can resolve it, exactly as search and open do for messages.
	// A short ref also masks a privacy @lid jid, which a raw provider id would leak.
	chatRows, err := req.Store.DB().QueryContext(ctx, `select jid from chats where trim(jid) <> '' order by jid`)
	if err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	defer func() { _ = chatRows.Close() }()
	for chatRows.Next() {
		var jid string
		if err := chatRows.Scan(&jid); err != nil {
			return nil, fmt.Errorf("scan chat ref for short refs: %w", err)
		}
		if ref := store.ChatRef(jid); ref != "" {
			records = append(records, trawlkit.ShortRefRecord{Ref: ref})
		}
	}
	if err := chatRows.Err(); err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	return records, nil
}

func searchAliases(ctx context.Context, req *trawlkit.Request, messages []store.Message) (map[string]string, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message))
	}
	return req.ShortRefAliases(ctx, refs)
}
