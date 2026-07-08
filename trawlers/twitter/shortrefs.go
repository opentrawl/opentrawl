package birdcrawl

import (
	"context"
	"errors"
	"fmt"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

var _ crawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *crawlkit.Request) ([]crawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select id from tweets where trim(id) <> '' order by id`)
	if err != nil {
		return nil, fmt.Errorf("read tweet refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []crawlkit.ShortRefRecord
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tweet ref for short refs: %w", err)
		}
		records = append(records, crawlkit.ShortRefRecord{Ref: store.TweetRef(id)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read tweet refs for short refs: %w", err)
	}
	return records, nil
}
