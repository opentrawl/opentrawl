package notes

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
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
	Folder    string                `json:"folder,omitempty"`
	Notes     []listNote            `json:"notes"`
	Folders   []archive.FolderCount `json:"folders"`
	Total     int64                 `json:"total"`
	Truncated bool                  `json:"truncated"`
}

func (c *Crawler) runList(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) > 1 {
		return usageError("list takes at most one folder name")
	}
	limit, err := ckflags.Limit(c.listLimit, true)
	if err != nil {
		return usageError(err.Error())
	}
	folder := ""
	if len(req.Args) == 1 {
		folder = strings.TrimSpace(req.Args[0])
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	if folder != "" {
		if err := checkKnownFolder(ctx, st, folder); err != nil {
			return err
		}
	}
	folders, err := st.FolderCounts(ctx, folder)
	if err != nil {
		return err
	}
	total := folderTotal(folders)
	items, err := st.ListNotes(ctx, folder, limit)
	if err != nil {
		return err
	}
	out := listOutput{
		Folder:    folder,
		Notes:     listNotes(items, listShortRefs(ctx, req, items)),
		Folders:   folders,
		Total:     total,
		Truncated: int64(len(items)) < total,
	}
	if req.Log != nil {
		_ = req.Log.Info("list_complete", fmt.Sprintf("returned=%d total=%d folders=%d", len(items), total, len(folders)))
	}
	if req.Format == output.JSON {
		return writeJSON(req.Out, out)
	}
	return printListText(req.Out, out)
}

// checkKnownFolder tells an unknown folder name from a real folder that
// simply has no notes right now. A typo in a folder name is a data error
// (exit 1, and the error names the real folders), not an empty result: an
// empty result and a mistyped name look identical to fmt.Fprintln unless
// something checks first.
func checkKnownFolder(ctx context.Context, st *archive.Store, folder string) error {
	known, err := st.KnownFolders(ctx)
	if err != nil {
		return err
	}
	for _, name := range known {
		if name == folder {
			return nil
		}
	}
	return commandErr("unknown_folder",
		fmt.Sprintf("no folder named %q", folder),
		"known folders: "+strings.Join(known, ", "),
		nil)
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
	for _, line := range render.Wrap(listSummary(out), render.OutputWidth(w)) {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if out.Truncated {
		if _, err := fmt.Fprintln(w, listContinuation(out)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Notes))
	for _, note := range out.Notes {
		rows = append(rows, []string{
			render.ShortLocalTime(parseContractTime(note.Modified)),
			note.Folder,
			note.Ref,
			listTitle(note.Title),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "modified"},
		{Header: "folder", Wrap: true},
		{Header: "ref"},
		{Header: "title", Wrap: true},
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

func folderTotal(folders []archive.FolderCount) int64 {
	var total int64
	for _, folder := range folders {
		total += folder.Notes
	}
	return total
}

// listSummary says how many rows are present, not just how large the archive
// is. The folder split teaches a reader which folder name they can pass back
// to the command.
func listSummary(out listOutput) string {
	shown := render.FormatInteger(int64(len(out.Notes)))
	total := render.FormatInteger(out.Total)
	if strings.TrimSpace(out.Folder) != "" {
		return fmt.Sprintf("Notes in %s: showing %s of %s, newest first.", out.Folder, shown, total)
	}
	foldersWord := "folders"
	if len(out.Folders) == 1 {
		foldersWord = "folder"
	}
	parts := make([]string, 0, len(out.Folders))
	for _, folder := range out.Folders {
		parts = append(parts, fmt.Sprintf("%s %s", folderName(folder.Folder), render.FormatInteger(folder.Notes)))
	}
	return fmt.Sprintf("Notes: showing %s of %s across %d %s, newest first: %s.",
		shown, total, len(out.Folders), foldersWord, strings.Join(parts, ", "))
}

func folderName(folder string) string {
	if strings.TrimSpace(folder) == "" {
		return "(no folder)"
	}
	return folder
}

func listContinuation(out listOutput) string {
	limit := len(out.Notes) * 2
	if int64(limit) > out.Total {
		limit = int(out.Total)
	}
	command := "More: trawl notes list"
	if strings.TrimSpace(out.Folder) != "" {
		command += " " + strconv.Quote(out.Folder)
	}
	return fmt.Sprintf("%s --limit %d", command, limit)
}
