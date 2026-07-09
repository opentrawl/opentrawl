package notes

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/attachfiles"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notesdb"
)

// resolveAttachments turns attachment rows read from the Notes database into
// archive insert rows, locating and copying each attachment's file (if any)
// from the Notes group container into the archive's attachments directory.
//
// groupContainerDir is the directory that holds NoteStore.sqlite on the
// original source, never the temp WAL-replay snapshot -- the snapshot only
// ever contains the sqlite file and WAL, not the sibling Media tree.
// archiveBaseDir is the directory that holds the archive database.
//
// Every attachment ends up with exactly one of three outcomes, and they are
// never conflated: no media (nothing was ever referenced, not an error), a
// copied file, or a file that was referenced but could not be found on disk
// (reported, never dropped).
func resolveAttachments(attachments []notesdb.Attachment, groupContainerDir, archiveBaseDir string) ([]archive.AttachmentInsert, error) {
	out := make([]archive.AttachmentInsert, 0, len(attachments))
	for _, att := range attachments {
		insert := archive.AttachmentInsert{
			ID:      att.ID,
			NoteID:  att.NoteID,
			MediaID: att.MediaID,
			Name:    att.Name,
			Type:    att.Type,
		}
		mediaID := strings.TrimSpace(att.MediaID)
		if !att.HasMedia {
			insert.Status = archive.AttachmentStatusNoFile
			out = append(out, insert)
			continue
		}
		if mediaID == "" {
			// The row references a media object that no longer exists.
			// That is corruption, so it lands in the warned-about missing
			// count, never in the normal no-file bucket.
			insert.Status = archive.AttachmentStatusMissing
			out = append(out, insert)
			continue
		}
		src, found, err := attachfiles.Locate(groupContainerDir, mediaID)
		if err != nil {
			return nil, err
		}
		if !found {
			insert.Status = archive.AttachmentStatusMissing
			out = append(out, insert)
			continue
		}
		relPath, size, err := attachfiles.Copy(archiveBaseDir, att.ID, src)
		if err != nil {
			return nil, err
		}
		insert.Status = archive.AttachmentStatusCopied
		insert.ArchivePath = relPath
		insert.SourceBytes = size
		out = append(out, insert)
	}
	return out, nil
}
