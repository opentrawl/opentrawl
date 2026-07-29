package gmail

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortReferenceAssignmentProvider = (*Crawler)(nil)

func (c *Crawler) RecordReferencesForShortReferenceAssignment(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	if req == nil || req.OpenedTrawlerArchiveStore == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select id from messages order by id`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortReferenceAssignmentCandidate
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: archive.RefPrefix + id})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	return records, nil
}
