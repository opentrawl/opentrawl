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
	Ref     string       `json:"ref"`
	Note    archive.Note `json:"note"`
	Version openVersion  `json:"version"`
	// Text is the note body for the version shown. VersionBody carries its
	// own Text too, but repeating it inside "version" would hand a consumer
	// the same bytes twice; openVersion drops that copy and this field is
	// the one that survives.
	Text string `json:"text,omitempty"`
}

// openVersion is the version metadata on an open card: everything about the
// recovered version except its body text, which openOutput.Text already
// carries once.
type openVersion struct {
	archive.Version
	Title  string `json:"title,omitempty"`
	Folder string `json:"folder,omitempty"`
}

func newOpenVersion(body archive.VersionBody) openVersion {
	return openVersion{Version: body.Version, Title: body.Title, Folder: body.Folder}
}

func (c *Crawler) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	value, err := c.loadOpenNote(ctx, req, ref)
	if err != nil {
		return err
	}
	out := openOutput{Ref: value.body.Ref, Note: value.note, Version: newOpenVersion(value.body), Text: value.body.Text}
	if req.Log != nil {
		_ = req.Log.Info("open_complete", "result=note_version")
	}
	if req.Format == output.JSON {
		return writeJSON(req.Out, out)
	}
	openRef := displayRef(ctx, req, cardRef(value.resolvedRef, value.note.ID, value.body.Ref))
	versionRef := displayRef(ctx, req, value.body.Ref)
	return printOpenText(req.Out, out, openRef, versionRef)
}

func (c *Crawler) loadOpenNote(ctx context.Context, req *trawlkit.Request, ref string) (openValue, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return openValue{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolvedRef, err := resolveInputRef(ctx, req, ref)
	if err != nil {
		return openValue{}, err
	}
	note, body, err := resolveOpen(ctx, st, resolvedRef)
	if err != nil {
		return openValue{}, err
	}
	if body.Title == "" {
		body.Title = note.Title
	}
	return openValue{resolvedRef: resolvedRef, note: note, body: body}, nil
}

// cardRef picks which ref the open card echoes. A reader who opened a note by
// its note ref (from list or search) sees that same note ref back; one who
// asked for a specific version by its version ref sees the version ref, so the
// handle on screen always reopens what they are looking at.
func cardRef(resolvedRef, noteID, versionRef string) string {
	if _, _, ok := archive.VersionFromRef(resolvedRef); ok {
		return versionRef
	}
	return archive.RefForNote(noteID)
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

// noteLabel names a note the way a human knows it: by title, never by the
// provider's note id.
func noteLabel(note archive.Note) string {
	if title := strings.TrimSpace(note.Title); title != "" {
		return title
	}
	return "(untitled note)"
}

// printOpenText writes the open card. openRef is what reopens what the
// reader asked for; versionRef is what reopens this exact recovered version.
// The two match when the reader opened a version ref directly, in which case
// one ref line says everything and the second would only repeat it.
func printOpenText(w io.Writer, out openOutput, openRef, versionRef string) error {
	title := noteLabel(out.Note)
	fields := []render.CardField{{Label: "Ref", Value: openRef}}
	if versionRef != "" && versionRef != openRef {
		fields = append(fields, render.CardField{Label: "Version", Value: versionRef})
	}
	fields = append(fields,
		render.CardField{Label: "Modified", Value: humanTime(out.Version.SourceModifiedAt)},
		render.CardField{Label: "Observed", Value: humanTime(out.Version.FirstObservedAt)},
		render.CardField{Label: "Source", Value: sourceLabel(out.Version.Version)},
	)
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
