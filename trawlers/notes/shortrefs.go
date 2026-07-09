package notes

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var _ trawlkit.ShortRefProvider = (*Crawler)(nil)
var _ trawlkit.ShortRefKindProvider = (*Crawler)(nil)

func (c *Crawler) ShortRefKinds() []string {
	return []string{archive.VersionRefPrefix}
}

func (c *Crawler) ShortRefRecords(ctx context.Context, req *trawlkit.Request) ([]trawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	rows, err := req.Store.DB().QueryContext(ctx, `
select note_id, zdata_sha256 from note_versions order by note_id, zdata_sha256`)
	if err != nil {
		return nil, fmt.Errorf("read version refs for short refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []trawlkit.ShortRefRecord
	for rows.Next() {
		var noteID, sha string
		if err := rows.Scan(&noteID, &sha); err != nil {
			return nil, fmt.Errorf("scan version ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: archive.RefForVersion(noteID, sha)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read version refs for short refs: %w", err)
	}
	return records, nil
}
