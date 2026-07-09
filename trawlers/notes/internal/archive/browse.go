package archive

import (
	"context"
	"strings"
)

// recentlyDeletedFolder is Apple's trash. A reader browsing their notes does not
// want deleted notes mixed in, so list, search and the folder summary all leave
// this folder out. The archive still keeps the versions, so an explicit open by
// ref still works.
const recentlyDeletedFolder = "Recently Deleted"

// NoteListItem is one row of the browse list: a note with the handful of facts
// a reader scans by. Ref is the note-level ref the shared index shortens.
type NoteListItem struct {
	Ref        string `json:"ref"`
	NoteID     string `json:"note_id"`
	Title      string `json:"title,omitempty"`
	Folder     string `json:"folder,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

// FolderCount is one folder and how many notes it holds, for the one-line
// summary above the list.
type FolderCount struct {
	Folder string `json:"folder"`
	Notes  int64  `json:"notes"`
}

// ListNotes returns real notes newest-modified first, leaving out the Recently
// Deleted folder and any note with no recovered body (an unfetched iCloud
// placeholder). When folder is set, the list is scoped to that one folder.
func (s *Store) ListNotes(ctx context.Context, folder string) ([]NoteListItem, error) {
	where, args := browseWhere(folder)
	rows, err := s.store.DB().QueryContext(ctx, `
select n.note_id, coalesce(n.title, ''), coalesce(n.folder, ''),
       coalesce(nullif(n.modified_at, ''), n.created_at)
from notes n
`+where+`
order by coalesce(nullif(n.modified_at, ''), n.created_at) desc, n.note_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []NoteListItem{}
	for rows.Next() {
		var item NoteListItem
		if err := rows.Scan(&item.NoteID, &item.Title, &item.Folder, &item.ModifiedAt); err != nil {
			return nil, err
		}
		item.Ref = RefForNote(item.NoteID)
		out = append(out, item)
	}
	return out, rows.Err()
}

// FolderCounts returns the note count per folder, most-populated first, over the
// same real notes ListNotes shows. When folder is set, only that folder.
func (s *Store) FolderCounts(ctx context.Context, folder string) ([]FolderCount, error) {
	where, args := browseWhere(folder)
	rows, err := s.store.DB().QueryContext(ctx, `
select coalesce(n.folder, ''), count(*)
from notes n
`+where+`
group by n.folder
order by count(*) desc, n.folder`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []FolderCount{}
	for rows.Next() {
		var fc FolderCount
		if err := rows.Scan(&fc.Folder, &fc.Notes); err != nil {
			return nil, err
		}
		out = append(out, fc)
	}
	return out, rows.Err()
}

// browseWhere builds the shared filter for the browse surfaces: real notes only
// (a recovered body exists), never the Recently Deleted folder, and one folder
// when the reader named it.
func browseWhere(folder string) (string, []any) {
	parts := []string{
		"exists (select 1 from note_versions v where v.note_id = n.note_id)",
		"coalesce(n.folder, '') <> '" + recentlyDeletedFolder + "'",
	}
	var args []any
	if folder = strings.TrimSpace(folder); folder != "" {
		parts = append(parts, "n.folder = ?")
		args = append(args, folder)
	}
	return "where " + strings.Join(parts, "\n  and "), args
}
