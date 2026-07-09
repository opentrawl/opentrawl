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

// ShortRefRecords indexes a short ref for every note and every recovered
// version. Browse and search hand back note-level refs, so each note needs a
// short ref of its own; the versions and at-time verbs still work in version
// refs, so those keep theirs. open resolves either, since both live in the one
// shared index.
func (c *Crawler) ShortRefRecords(ctx context.Context, req *trawlkit.Request) ([]trawlkit.ShortRefRecord, error) {
	if req == nil || req.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	var records []trawlkit.ShortRefRecord
	noteRows, err := req.Store.DB().QueryContext(ctx, `select note_id from notes order by note_id`)
	if err != nil {
		return nil, fmt.Errorf("read note refs for short refs: %w", err)
	}
	defer func() { _ = noteRows.Close() }()
	for noteRows.Next() {
		var noteID string
		if err := noteRows.Scan(&noteID); err != nil {
			return nil, fmt.Errorf("scan note ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: archive.RefForNote(noteID)})
	}
	if err := noteRows.Err(); err != nil {
		return nil, fmt.Errorf("read note refs for short refs: %w", err)
	}
	versionRows, err := req.Store.DB().QueryContext(ctx, `
select note_id, zdata_sha256 from note_versions order by note_id, zdata_sha256`)
	if err != nil {
		return nil, fmt.Errorf("read version refs for short refs: %w", err)
	}
	defer func() { _ = versionRows.Close() }()
	for versionRows.Next() {
		var noteID, sha string
		if err := versionRows.Scan(&noteID, &sha); err != nil {
			return nil, fmt.Errorf("scan version ref for short refs: %w", err)
		}
		records = append(records, trawlkit.ShortRefRecord{Ref: archive.RefForVersion(noteID, sha)})
	}
	if err := versionRows.Err(); err != nil {
		return nil, fmt.Errorf("read version refs for short refs: %w", err)
	}
	return records, nil
}
