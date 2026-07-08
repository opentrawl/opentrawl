package notes

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
)

type openOutput struct {
	Ref     string              `json:"ref"`
	Note    archive.Note        `json:"note"`
	Version archive.VersionBody `json:"version"`
	Text    string              `json:"text,omitempty"`
}

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	note, body, err := resolveOpen(ctx, st, ref)
	if err != nil {
		return err
	}
	if body.Title == "" {
		body.Title = note.Title
	}
	out := openOutput{Ref: body.Ref, Note: note, Version: body, Text: body.Text}
	if req.Log != nil {
		_ = req.Log.Info("open_complete", "result=note_version")
	}
	if req.Format == output.JSON {
		return writeJSON(req.Out, out)
	}
	return printOpenText(req.Out, out)
}

func resolveOpen(ctx context.Context, st *archive.Store, ref string) (archive.Note, archive.VersionBody, error) {
	ref = strings.TrimSpace(ref)
	if noteID, sha, ok := archive.VersionFromRef(ref); ok {
		note, err := st.ResolveNote(ctx, noteID)
		if err != nil {
			return archive.Note{}, archive.VersionBody{}, err
		}
		body, err := st.VersionBody(ctx, note.ID, sha)
		return note, body, err
	}
	note, err := st.ResolveNote(ctx, ref)
	if err != nil {
		return archive.Note{}, archive.VersionBody{}, err
	}
	body, err := st.VersionBody(ctx, note.ID, "")
	return note, body, err
}

func printOpenText(w io.Writer, out openOutput) error {
	title := strings.TrimSpace(out.Note.Title)
	if title == "" {
		title = "(untitled note)"
	}
	fields := []render.CardField{
		{Label: "Ref", Value: out.Ref},
		{Label: "Note", Value: out.Note.ID},
		{Label: "Version", Value: out.Version.ShortSHA},
		{Label: "Modified", Value: out.Version.SourceModifiedAt},
		{Label: "Observed", Value: out.Version.FirstObservedAt},
		{Label: "Source", Value: sourceLabel(out.Version.Version)},
	}
	body := out.Text
	hints := []string{}
	if out.Version.TextStatus != "decoded" {
		body = "This note body cannot yet be projected to text."
		hints = append(hints, out.Version.Unsupported)
	}
	return render.WriteCard(w, render.Card{Title: title, Fields: fields, Body: body, Hints: hints})
}

func sourceLabel(version archive.Version) string {
	source := strings.TrimSpace(version.Source)
	detail := strings.TrimSpace(version.SourceDetail)
	if source == "" {
		return detail
	}
	if detail == "" {
		return source
	}
	return source + ":" + detail
}
