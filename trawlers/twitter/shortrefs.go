package twitter

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

var _ trawlkit.ShortReferenceAssignmentProvider = (*Crawler)(nil)

func (c *Crawler) RecordReferencesForShortReferenceAssignment(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	if req == nil || req.OpenedTrawlerArchiveStore == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select id from tweets where trim(id) <> '' order by id`)
	if err != nil {
		return nil, fmt.Errorf("read tweet refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortReferenceAssignmentCandidate
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tweet ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: store.TweetRef(id)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read tweet refs for short refs: %w", err)
	}
	return records, nil
}
