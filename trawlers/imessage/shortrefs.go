package imsgcrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

var _ crawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *crawlkit.Request) ([]crawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select source_rowid from messages order by source_rowid`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []crawlkit.ShortRefRecord
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, crawlkit.ShortRefRecord{Ref: archive.MessageRef(strconv.FormatInt(id, 10))})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	return records, nil
}
