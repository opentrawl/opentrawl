package calcrawl

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortRefProvider = (*Crawler)(nil)
var _ trawlkit.ShortRefKindProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefKinds() []string {
	return []string{archive.EventRefPrefix}
}

func (c *Crawler) ShortRefRecords(ctx context.Context, req *trawlkit.Request) ([]trawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select event_uid from events order by event_uid`)
	if err != nil {
		return nil, fmt.Errorf("read event refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortRefRecord
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan event ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: archive.RefForUID(uid)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read event refs for short refs: %w", err)
	}
	return records, nil
}
