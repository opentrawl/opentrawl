package archive

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (s *Store) ShortRefRecords(ctx context.Context) ([]trawlkit.ShortRefRecord, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select id from people order by id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := []trawlkit.ShortRefRecord{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: PersonRef(id)})
	}
	return records, rows.Err()
}
