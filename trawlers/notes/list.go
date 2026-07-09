package notes

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type listNote struct {
	Ref      string `json:"ref"`
	NoteID   string `json:"note_id"`
	Title    string `json:"title,omitempty"`
	Folder   string `json:"folder,omitempty"`
	Modified string `json:"modified,omitempty"`
}

type listOutput struct {
	Folder  string                `json:"folder,omitempty"`
	Notes   []listNote            `json:"notes"`
	Folders []archive.FolderCount `json:"folders"`
}

func (c *Crawler) runList(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) > 1 {
		return usageError("list takes at most one folder name")
	}
	folder := ""
	if len(req.Args) == 1 {
		folder = strings.TrimSpace(req.Args[0])
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	items, err := st.ListNotes(ctx, folder)
	if err != nil {
		return err
	}
	folders, err := st.FolderCounts(ctx, folder)
	if err != nil {
		return err
	}
	out := listOutput{Folder: folder, Notes: listNotes(items, listShortRefs(ctx, req, items)), Folders: folders}
	if req.Log != nil {
		_ = req.Log.Info("list_complete", fmt.Sprintf("notes=%d folders=%d", len(items), len(folders)))
	}
	if req.Format == output.JSON {
		return writeJSON(req.Out, out)
	}
	return printListText(req.Out, out)
}

func listNotes(items []archive.NoteListItem, shortRefs map[string]string) []listNote {
	out := make([]listNote, 0, len(items))
	for _, item := range items {
		out = append(out, listNote{
			Ref:      refOrShort(shortRefs, item.Ref),
			NoteID:   item.NoteID,
			Title:    item.Title,
			Folder:   item.Folder,
			Modified: item.ModifiedAt,
		})
	}
	return out
}

// listShortRefs maps each note's full ref to its short ref from the shared
// index, so the list column shows the short ref a reader pastes into open. A
// ref with no alias yet falls back to the full ref.
func listShortRefs(ctx context.Context, req *trawlkit.Request, items []archive.NoteListItem) map[string]string {
	refs := make([]string, 0, len(items))
	for _, item := range items {
		refs = append(refs, item.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		return nil
	}
	return aliases
}

func printListText(w io.Writer, out listOutput) error {
	if len(out.Notes) == 0 {
		_, err := fmt.Fprintln(w, listEmpty(out.Folder))
		return err
	}
	if _, err := fmt.Fprintln(w, folderSummary(out.Folders, out.Folder)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Notes))
	for _, note := range out.Notes {
		rows = append(rows, []string{
			render.ShortLocalTime(parseContractTime(note.Modified)),
			note.Folder,
			listTitle(note.Title),
			note.Ref,
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "modified"},
		{Header: "folder"},
		{Header: "title"},
		{Header: "ref"},
	}, rows)
}

func listTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return "(untitled note)"
	}
	return title
}

func listEmpty(folder string) string {
	if strings.TrimSpace(folder) != "" {
		return fmt.Sprintf("No notes in %s.", folder)
	}
	return "No notes yet."
}

// folderSummary is the one line above the table. Scoped to a folder it names
// that folder's count; otherwise it names the total and the per-folder split, so
// a reader sees where their notes live without a separate folders verb.
func folderSummary(folders []archive.FolderCount, scoped string) string {
	var total int64
	for _, f := range folders {
		total += f.Notes
	}
	notesWord := "notes"
	if total == 1 {
		notesWord = "note"
	}
	if strings.TrimSpace(scoped) != "" {
		return fmt.Sprintf("%s %s in %s, newest first.", render.FormatInteger(total), notesWord, scoped)
	}
	parts := make([]string, 0, len(folders))
	for _, f := range folders {
		parts = append(parts, fmt.Sprintf("%s %s", folderName(f.Folder), render.FormatInteger(f.Notes)))
	}
	foldersWord := "folders"
	if len(folders) == 1 {
		foldersWord = "folder"
	}
	return fmt.Sprintf("%s %s across %d %s, newest first: %s.",
		render.FormatInteger(total), notesWord, len(folders), foldersWord, strings.Join(parts, ", "))
}

func folderName(folder string) string {
	if strings.TrimSpace(folder) == "" {
		return "(no folder)"
	}
	return folder
}
