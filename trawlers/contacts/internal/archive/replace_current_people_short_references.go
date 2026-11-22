package archive

import (
	"context"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (s *Store) ListShortReferenceCandidates(
	ctx context.Context,
) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	rows, err := s.database().QueryContext(ctx, `select id from people order by id`)
	if err != nil {
		return nil, fmt.Errorf("read current Contacts people for short reference assignment: %w", err)
	}
	candidates := []trawlkit.ShortReferenceAssignmentCandidate{}
	for rows.Next() {
		var personID string
		if err := rows.Scan(&personID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read current Contacts person identity for short reference assignment: %w", err)
		}
		candidates = append(
			candidates,
			trawlkit.ShortReferenceAssignmentCandidate{
				StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
					PersonRef(personID),
				),
			},
		)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("read current Contacts people for short reference assignment: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close current Contacts people short reference assignment rows: %w", err)
	}
	return candidates, nil
}
