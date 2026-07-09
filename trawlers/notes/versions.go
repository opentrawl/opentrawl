package notes

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type versionListOutput struct {
	Note     archive.Note      `json:"note"`
	Versions []archive.Version `json:"versions"`
}

func (c *Crawler) runVersions(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 1 {
		return usageError("versions needs one note identifier, ref or title prefix")
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	inputRef, err := resolveInputRef(ctx, req, req.Args[0])
	if err != nil {
		return err
	}
	note, err := st.ResolveNote(ctx, inputRef)
	if err != nil {
		return err
	}
	versions, err := st.Versions(ctx, note.ID)
	if err != nil {
		return err
	}
	out := versionListOutput{Note: note, Versions: versions}
	if req.Format == output.JSON {
		return writeJSON(req.Out, out)
	}
	return printVersionsText(req.Out, out, versionShortRefs(ctx, req, versions))
}

// versionShortRefs maps each version's full ref to its short ref so the human
// table shows the short ref users pass back to open. Refs with no alias fall
// back to the full ref.
func versionShortRefs(ctx context.Context, req *trawlkit.Request, versions []archive.Version) map[string]string {
	refs := make([]string, 0, len(versions))
	for _, version := range versions {
		refs = append(refs, version.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		return nil
	}
	return aliases
}

func (c *Crawler) runAtTime(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 1 {
		return usageError("at-time needs one note identifier, ref or title prefix")
	}
	if strings.TrimSpace(c.atTimeRaw) == "" {
		return usageError("at-time requires --time")
	}
	requested, err := ckflags.Date(c.atTimeRaw)
	if err != nil {
		return usageError("--time: " + err.Error())
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	inputRef, err := resolveInputRef(ctx, req, req.Args[0])
	if err != nil {
		return err
	}
	note, err := st.ResolveNote(ctx, inputRef)
	if err != nil {
		return err
	}
	result, err := st.AtTime(ctx, note, requested)
	if err != nil {
		return err
	}
	if req.Format == output.JSON {
		return writeJSON(req.Out, result)
	}
	cardRef := ""
	if result.Version != nil {
		cardRef = displayRef(ctx, req, result.Version.Ref)
	}
	return printAtTimeText(req.Out, result, cardRef)
}

func refOrShort(shortRefs map[string]string, fullRef string) string {
	if alias := shortRefs[fullRef]; alias != "" {
		return alias
	}
	return fullRef
}

func printVersionsText(w io.Writer, out versionListOutput, shortRefs map[string]string) error {
	if len(out.Versions) == 0 {
		_, err := fmt.Fprintf(w, "No recovered versions for %s.\n", noteLabel(out.Note))
		return err
	}
	if _, err := fmt.Fprintln(w, versionsHeading(out)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Versions))
	for _, version := range out.Versions {
		rows = append(rows, []string{
			refOrShort(shortRefs, version.Ref),
			humanTime(version.SourceModifiedAt),
			humanTime(version.FirstObservedAt),
			sourceLabel(version),
		})
	}
	// The content hash used to live here as a "version" column, but it is not
	// a ref a reader can type back to open — ref is, so ref leads and the raw
	// hash is gone. It still travels in JSON (sha256, short_sha) for anyone
	// scripting against the archive directly.
	return render.WriteTable(w, []render.TableColumn{
		{Header: "ref"},
		{Header: "modified"},
		{Header: "observed"},
		{Header: "source"},
	}, rows)
}

// versionsHeading names the note and how many recovered versions it has,
// matching the voice of the list and search headers above their tables.
func versionsHeading(out versionListOutput) string {
	word := "versions"
	if len(out.Versions) == 1 {
		word = "version"
	}
	return fmt.Sprintf("%s recovered %s of %s, newest first.",
		render.FormatInteger(int64(len(out.Versions))), word, noteLabel(out.Note))
}

func printAtTimeText(w io.Writer, result archive.AtTimeResult, cardRef string) error {
	if result.Version == nil {
		_, err := fmt.Fprintf(w, "No recovered version for %s at or before %s.\n%s\n", noteLabel(result.Note), humanTime(result.RequestedTime), result.Gap)
		return err
	}
	title := noteLabel(result.Note)
	// Ref is already the typeable short ref for this exact recovered version
	// (see runAtTime); the raw content hash carried no action of its own, so
	// it does not get a second row here.
	fields := []render.CardField{
		{Label: "Match", Value: result.Match},
		{Label: "Requested", Value: humanTime(result.RequestedTime)},
		{Label: "Ref", Value: cardRef},
		{Label: "Modified", Value: humanTime(result.Version.SourceModifiedAt)},
		{Label: "Source", Value: sourceLabel(result.Version.Version)},
	}
	body := result.Version.Text
	hints := []string{"Open: trawl notes open " + cardRef}
	if result.Version.TextStatus != "decoded" {
		body = "This note body cannot yet be projected to text."
		hints = append(hints, result.Version.Unsupported)
	}
	return render.WriteCard(w, render.Card{Title: title, Fields: fields, Body: body, Hints: hints})
}
