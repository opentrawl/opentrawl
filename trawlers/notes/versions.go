package notes

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit"
	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
)

type versionListOutput struct {
	Note     archive.Note      `json:"note"`
	Versions []archive.Version `json:"versions"`
}

func (c *Crawler) runVersions(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 1 {
		return usageError("versions needs one note identifier, ref or title prefix")
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	note, err := st.ResolveNote(ctx, req.Args[0])
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
	return printVersionsText(req.Out, out)
}

func (c *Crawler) runAtTime(ctx context.Context, req *crawlkit.Request) error {
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
	note, err := st.ResolveNote(ctx, req.Args[0])
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
	return printAtTimeText(req.Out, result)
}

func printVersionsText(w io.Writer, out versionListOutput) error {
	rows := make([][]string, 0, len(out.Versions))
	for _, version := range out.Versions {
		rows = append(rows, []string{
			version.ShortSHA,
			version.SourceModifiedAt,
			version.FirstObservedAt,
			sourceLabel(version),
			version.Ref,
		})
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "No recovered versions for %s.\n", out.Note.ID)
		return err
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "version"},
		{Header: "modified"},
		{Header: "observed"},
		{Header: "source"},
		{Header: "ref", Wrap: true},
	}, rows)
}

func printAtTimeText(w io.Writer, result archive.AtTimeResult) error {
	if result.Version == nil {
		_, err := fmt.Fprintf(w, "No recovered version for %s at or before %s.\n%s\n", result.Note.ID, result.RequestedTime, result.Gap)
		return err
	}
	title := strings.TrimSpace(result.Note.Title)
	if title == "" {
		title = "(untitled note)"
	}
	fields := []render.CardField{
		{Label: "Match", Value: result.Match},
		{Label: "Requested", Value: result.RequestedTime},
		{Label: "Ref", Value: result.Version.Ref},
		{Label: "Version", Value: result.Version.ShortSHA},
		{Label: "Modified", Value: result.Version.SourceModifiedAt},
		{Label: "Source", Value: sourceLabel(result.Version.Version)},
	}
	body := result.Version.Text
	hints := []string{"Open: trawl notes open " + result.Version.Ref}
	if result.Version.TextStatus != "decoded" {
		body = "This note body cannot yet be projected to text."
		hints = append(hints, result.Version.Unsupported)
	}
	return render.WriteCard(w, render.Card{Title: title, Fields: fields, Body: body, Hints: hints})
}
