package notes

import (
	"context"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
)

func (c *Crawler) ExportSearchableRecordPage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	exportRequest *searchablerecord.SearchableRecordExportRequest,
) (*searchablerecord.TrawlerSearchableRecordExportPage, error) {
	recordsAfterNoteIdentifier := ""
	recordsAfterVersionSHA256 := ""
	recordsAfterCanonicalReference := trawlkit.CanonicalArchiveRecordReferenceText(exportRequest.GetRecordsAfterCanonicalRecordReference())
	if recordsAfterCanonicalReference != "" {
		var valid bool
		recordsAfterNoteIdentifier, recordsAfterVersionSHA256, valid = archive.VersionFromRef(recordsAfterCanonicalReference)
		if !valid {
			return nil, fmt.Errorf("notes searchable record cursor is invalid")
		}
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `
		select version.note_id, version.zdata_sha256,
		       version.source_modified_at, version.first_observed_at,
		       coalesce(note.title, ''), coalesce(note.folder, ''),
		       coalesce(note.created_at, ''), coalesce(note.modified_at, ''),
		       (select count(*) from note_versions counted_version where counted_version.note_id = version.note_id),
		       version.text
		from note_versions version
		left join notes note on note.note_id = version.note_id
		where (version.note_id > ? or (version.note_id = ? and version.zdata_sha256 > ?))
		  and trim(coalesce(note.title, '') || coalesce(version.text, '')) <> ''
		order by version.note_id, version.zdata_sha256
		limit ?`, recordsAfterNoteIdentifier, recordsAfterNoteIdentifier, recordsAfterVersionSHA256, int64(exportRequest.GetMaximumRecordCount())+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	searchableArchiveRecords := make([]*searchablerecord.SearchableArchiveRecord, 0, exportRequest.GetMaximumRecordCount()+1)
	for rows.Next() {
		var noteIdentifier, versionSHA256 string
		var openedNoteValues openedNoteValuesLoadedFromNotesArchive
		if err := rows.Scan(
			&noteIdentifier, &versionSHA256,
			&openedNoteValues.openedNoteVersionBody.SourceModifiedAt,
			&openedNoteValues.openedNoteVersionBody.FirstObservedAt,
			&openedNoteValues.archivedNote.Title,
			&openedNoteValues.archivedNote.Folder,
			&openedNoteValues.archivedNote.CreatedAt,
			&openedNoteValues.archivedNote.ModifiedAt,
			&openedNoteValues.archivedNote.VersionCount,
			&openedNoteValues.openedNoteVersionBody.Text,
		); err != nil {
			return nil, err
		}
		openedNoteValues.archivedNote.ID = noteIdentifier
		openedNoteValues.canonicalOpenedNoteRecordReference = archive.RefForVersion(noteIdentifier, versionSHA256)
		openedNoteValues.openedNoteVersionBody.Ref = openedNoteValues.canonicalOpenedNoteRecordReference
		openedNoteValues.openedNoteVersionBody.NoteID = noteIdentifier
		openedNoteValues.openedNoteVersionBody.SHA256 = versionSHA256
		openedNoteValues.openedNoteVersionBody.TextStatus = "decoded"
		openedNoteValues.openedNoteVersionBody.Title = openedNoteValues.archivedNote.Title
		typedRecord, canonicalOpenedRecordReference := projectOpenedNoteRecord(openedNoteValues)
		searchableArchiveRecords = append(searchableArchiveRecords, &searchablerecord.SearchableArchiveRecord{
			CanonicalRecordReference:            trawlkit.NewCanonicalArchiveRecordReference(canonicalOpenedRecordReference),
			CanonicalSearchResultGroupReference: typedRecord.GetCanonicalNoteRecordReference(),
			TypedSourceRecord: &searchablerecord.SearchableArchiveRecord_OpenedNoteRecord{
				OpenedNoteRecord: typedRecord,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return trawlkit.CompleteSearchableRecordExportPage(searchableArchiveRecords, exportRequest.GetMaximumRecordCount()), nil
}
