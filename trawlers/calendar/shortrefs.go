package calcrawl

import (
	"context"
	"errors"
	"fmt"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

var _ crawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *crawlkit.Request) ([]crawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select event_uid from events order by event_uid`)
	if err != nil {
		return nil, fmt.Errorf("read event refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []crawlkit.ShortRefRecord
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan event ref for short refs: %w", err)
		}
		records = append(records, crawlkit.ShortRefRecord{Ref: archive.RefForUID(uid)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read event refs for short refs: %w", err)
	}
	return records, nil
}
