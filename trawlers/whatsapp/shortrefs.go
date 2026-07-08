package wacrawl

import (
	"context"
	"errors"
	"fmt"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/wacrawl/internal/store"
)

var _ crawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *crawlkit.Request) ([]crawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select msg_id from messages where trim(msg_id) <> '' order by msg_id`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []crawlkit.ShortRefRecord
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, crawlkit.ShortRefRecord{Ref: messageRefPrefix + id})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	return records, nil
}

func searchAliases(ctx context.Context, req *crawlkit.Request, messages []store.Message) (map[string]string, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message))
	}
	return req.ShortRefAliases(ctx, refs)
}
