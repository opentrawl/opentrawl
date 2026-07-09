package imsgcrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *trawlkit.Request) ([]trawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select source_rowid from messages order by source_rowid`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortRefRecord
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: archive.MessageRef(strconv.FormatInt(id, 10))})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	// Chat refs join the same index so the chats table can show a short ref and
	// messages --chat can resolve it, exactly as search and open do for messages.
	chatRows, err := req.Store.DB().QueryContext(ctx, `select source_rowid from chats order by source_rowid`)
	if err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	defer func() { _ = chatRows.Close() }()
	for chatRows.Next() {
		var id int64
		if err := chatRows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan chat ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: archive.ChatRef(strconv.FormatInt(id, 10))})
	}
	if err := chatRows.Err(); err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	return records, nil
}
