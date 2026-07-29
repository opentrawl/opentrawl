package imessage

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortReferenceAssignmentProvider = (*Crawler)(nil)

func (c *Crawler) RecordReferencesForShortReferenceAssignment(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	if req == nil || req.OpenedTrawlerArchiveStore == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select source_rowid from messages order by source_rowid`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortReferenceAssignmentCandidate
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: archive.MessageRef(strconv.FormatInt(id, 10))})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	// Canonical conversation references join the same index so TrawlKit can
	// compose a global conversation link and resolve its local component after
	// the host selects iMessage.
	chatRows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select source_rowid from chats order by source_rowid`)
	if err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	defer func() { _ = chatRows.Close() }()
	for chatRows.Next() {
		var id int64
		if err := chatRows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan chat ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: archive.ChatRef(strconv.FormatInt(id, 10))})
	}
	if err := chatRows.Err(); err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	return records, nil
}
