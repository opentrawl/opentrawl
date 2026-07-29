package archive

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (s *Store) RecordReferencesForShortReferenceAssignment(ctx context.Context) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	rows, err := s.database().QueryContext(ctx, `select id from people order by id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := []trawlkit.ShortReferenceAssignmentCandidate{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		records = append(records, trawlkit.ShortReferenceAssignmentCandidate{StableRecordReferenceUsedForShortReferenceAssignment: PersonRef(id)})
	}
	return records, rows.Err()
}
