package archive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/archivetime"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

// ExportArchiveIndexChanges streams all latest Apple Notes plus historical
// note-version short references into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var noteCount, versionCount int64
	var maxMod string
	if err := s.store.DB().QueryRowContext(
		ctx,
		`select count(*), coalesce(max(modified_at), '') from notes`,
	).Scan(&noteCount, &maxMod); err != nil {
		return fmt.Errorf("read notes index cursor state: %w", err)
	}
	if err := s.store.DB().QueryRowContext(ctx, `select count(*) from note_versions`).Scan(&versionCount); err != nil {
		return fmt.Errorf("count note versions for index cursor: %w", err)
	}
	nextCursor := fmt.Sprintf("notes:%d:%d:%s", noteCount, versionCount, maxMod)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	versionRefs, err := s.loadNoteVersionShortRefs(ctx)
	if err != nil {
		return err
	}

	rows, err := s.store.DB().QueryContext(ctx, `
		select n.note_id, n.title, n.folder, n.modified_at, coalesce(v.text, '')
		from notes n
		left join note_versions v on v.rowid = (
			select rowid from note_versions v2
			where v2.note_id = n.note_id
			order by v2.source_modified_at desc, v2.rowid desc
			limit 1
		)
		order by n.note_id
	`)
	if err != nil {
		return fmt.Errorf("read notes for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, noteCount)
	for rows.Next() {
		var noteID, title, folder, modifiedAt, body string
		if err := rows.Scan(&noteID, &title, &folder, &modifiedAt, &body); err != nil {
			return fmt.Errorf("scan note for unified index: %w", err)
		}
		ref := trawlkit.NewCanonicalArchiveRecordReference(RefForNote(noteID))
		modUnix, hasMod := parseNoteISOTime(modifiedAt)
		var labels []string
		if trimmedFolder := strings.TrimSpace(folder); trimmedFolder != "" {
			labels = []string{trimmedFolder}
		}
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          ref,
			ResolvedRecordReference:        ref,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_NOTE,
			ConversationOrThreadIdentifier: RefForNote(noteID),
			DisplayContextTitle:            strings.TrimSpace(folder),
			OccurredUnixSeconds:            modUnix,
			HasOccurredTime:                hasMod,
			HasTimeOfDay:                   true,
			Title:                          strings.TrimSpace(title),
			BodyText:                       strings.TrimSpace(body),
			ObservedSourceLabels:           labels,
			MechanicalJoinAnchors:          buildNoteAnchors(title, body),
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return emit(&indexcontract.ArchiveIndexChangeBatch{
		Upserts:                     upserts,
		ShortReferenceOnlyEntries:   versionRefs,
		CompleteSnapshotReplacement: true,
		NextOpaqueCursor:            nextCursor,
	})
}

func (s *Store) loadNoteVersionShortRefs(ctx context.Context) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select note_id, zdata_sha256 from note_versions order by note_id, zdata_sha256`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var entries []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var noteID, sha string
		if err := rows.Scan(&noteID, &sha); err != nil {
			return nil, err
		}
		verRef := trawlkit.NewCanonicalArchiveRecordReference(RefForVersion(noteID, sha))
		entries = append(entries, &indexcontract.ShortReferenceOnlyRegistrationEntry{
			StableRecordReference:   verRef,
			ResolvedRecordReference: verRef,
		})
	}
	return entries, rows.Err()
}

func parseNoteISOTime(raw string) (int64, bool) {
	if unixSeconds, ok := archivetime.ParseRFC3339UnixSeconds(raw); ok {
		return unixSeconds, true
	}
	trimmed := strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

func buildNoteAnchors(title, body string) []*indexcontract.MechanicalJoinAnchor {
	var out []*indexcontract.MechanicalJoinAnchor
	for _, dom := range unifiedindex.ExtractRegistrableDomains(title + " " + unifiedindex.TruncateRunes(body, 2000)) {
		out = append(out, &indexcontract.MechanicalJoinAnchor{
			AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_REGISTRABLE_DOMAIN,
			AnchorValue: dom,
		})
	}
	return out
}
