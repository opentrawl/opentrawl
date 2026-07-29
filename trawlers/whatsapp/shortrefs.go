package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortReferenceAssignmentProvider = (*Crawler)(nil)

func (c *Crawler) RecordReferencesForShortReferenceAssignment(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	if req == nil || req.OpenedTrawlerArchiveStore == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select msg_id from messages where trim(msg_id) <> '' order by msg_id`)
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
		records = append(records, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: messageRefPrefix + id})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs for short refs: %w", err)
	}
	// Canonical conversation references join the same index so TrawlKit can
	// compose a global conversation link and resolve its local component after
	// the host selects WhatsApp. The link also keeps a private @lid JID out of
	// human output.
	chatRows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `select jid from chats where trim(jid) <> '' order by jid`)
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
			records = append(records, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: ref})
		}
	}
	if err := chatRows.Err(); err != nil {
		return nil, fmt.Errorf("read chat refs for short refs: %w", err)
	}
	return records, nil
}
