package photoscrawl

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortRefProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefRecords(ctx context.Context, req *trawlkit.Request) ([]trawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `select id from asset order by id`)
	if err != nil {
		return nil, fmt.Errorf("read asset refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortRefRecord
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan asset ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: archive.AssetRef(id)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read asset refs for short refs: %w", err)
	}
	return records, nil
}
