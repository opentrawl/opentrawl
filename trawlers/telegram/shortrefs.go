package telecrawl

import (
	"context"
	"errors"
	"fmt"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/telecrawl/internal/store"
)

var _ crawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *crawlkit.Request) ([]crawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select source_pk from messages order by source_pk`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []crawlkit.ShortRefRecord
	for rows.Next() {
		var sourcePK int64
		if err := rows.Scan(&sourcePK); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, crawlkit.ShortRefRecord{Ref: store.MessageRef(sourcePK)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	return records, nil
}

func shortRefsForMessages(ctx context.Context, req *crawlkit.Request, messages []store.Message) (map[string]string, error) {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message.SourcePK))
	}
	return req.ShortRefAliases(ctx, refs)
}
