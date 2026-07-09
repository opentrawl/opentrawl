package notes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type openOutput struct {
	Ref     string              `json:"ref"`
	Note    archive.Note        `json:"note"`
	Version archive.VersionBody `json:"version"`
	Text    string              `json:"text,omitempty"`
}

func (c *Crawler) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolvedRef, err := resolveInputRef(ctx, req, ref)
	if err != nil {
		return err
	}
	note, body, err := resolveOpen(ctx, st, resolvedRef)
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
	return printOpenText(req.Out, out, displayRef(ctx, req, body.Ref))
}

// resolveInputRef turns a short ref from search into its full version ref.
// Apple note IDs are uppercase UUIDs and never look like short refs, so they
// pass through unchanged. A short-ref-shaped input that matches nothing in the
// index also passes through so ResolveNote can try it as a title prefix; one
// that does match resolves as a short ref — short refs take precedence over
// title prefixes that happen to share their shape.
func resolveInputRef(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !trawlkit.ValidShortRef(ref) {
		return ref, nil
	}
	matches, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return ref, nil
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr("ambiguous_short_ref", "short ref matches more than one note version", "rerun search or use the full ref", err)
	}
	if err != nil {
		return "", err
	}
	return matches[0], nil
}

// displayRef returns the short ref for a full version ref, falling back to the
// full ref when the short-ref index has no alias for it.
func displayRef(ctx context.Context, req *trawlkit.Request, fullRef string) string {
	aliases, err := req.ShortRefAliases(ctx, []string{fullRef})
	if err != nil {
		return fullRef
	}
	if alias := aliases[fullRef]; alias != "" {
		return alias
	}
	return fullRef
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

func printOpenText(w io.Writer, out openOutput, cardRef string) error {
	title := strings.TrimSpace(out.Note.Title)
	if title == "" {
		title = "(untitled note)"
	}
	fields := []render.CardField{
		{Label: "Ref", Value: cardRef},
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
