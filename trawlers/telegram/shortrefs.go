package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortReferenceAssignmentProvider = (*Crawler)(nil)

func (c *Crawler) RecordReferencesForShortReferenceAssignment(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	if req == nil || req.OpenedTrawlerArchiveStore == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select source_pk from messages order by source_pk`)
	if err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortReferenceAssignmentCandidate
	for rows.Next() {
		var sourcePK int64
		if err := rows.Scan(&sourcePK); err != nil {
			return nil, fmt.Errorf("scan message ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortReferenceAssignmentCandidate{
			StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
				store.MessageRef(sourcePK),
			),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	// Canonical conversation references join the same index so TrawlKit can
	// compose a global conversation link and resolve its local component after
	// the host selects Telegram.
	chatRows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select cast(id as text) from chats order by id`)
	if err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	defer func() { _ = chatRows.Close() }()
	for chatRows.Next() {
		var jid string
		if err := chatRows.Scan(&jid); err != nil {
			return nil, fmt.Errorf("scan chat ref for short refs: %w", err)
		}
		if ref := store.ChatRef(jid); ref != "" {
			records = append(records, trawlkit.ShortReferenceAssignmentCandidate{
				StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(ref),
			})
		}
	}
	if err := chatRows.Err(); err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	return records, nil
}
