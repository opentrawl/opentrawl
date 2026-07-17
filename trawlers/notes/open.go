package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

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
