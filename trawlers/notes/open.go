package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenNote(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (openedNoteValuesLoadedFromNotesArchive, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return openedNoteValuesLoadedFromNotesArchive{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	canonicalRecordReferences, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return openedNoteValuesLoadedFromNotesArchive{}, commandErr(
			"ambiguous_short_ref",
			"More than one note version has that link.",
			err,
		)
	}
	if err != nil {
		return openedNoteValuesLoadedFromNotesArchive{}, err
	}
	resolvedRef := trawlkit.CanonicalArchiveRecordReferenceText(canonicalRecordReferences[0])
	note, body, err := resolveOpen(ctx, st, resolvedRef)
	if err != nil {
		return openedNoteValuesLoadedFromNotesArchive{}, noteLookupErrorForOpen(err)
	}
	if body.Title == "" {
		body.Title = note.Title
	}
	return openedNoteValuesLoadedFromNotesArchive{
		canonicalOpenedNoteRecordReference: resolvedRef,
		archivedNote:                       note,
		openedNoteVersionBody:              body,
	}, nil
}

// resolveInputRef turns a displayed globally routable Notes link or local short ref
// into its canonical record reference.
// Apple note IDs are uppercase UUIDs and never look like short refs, so they
// pass through unchanged. A local short-ref-shaped input that matches nothing
// in the index also passes through so ResolveNote can try it as a title prefix.
// A match resolves as a short ref, so short refs take precedence over title
// prefixes that happen to share their shape.
func resolveInputRef(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (string, error) {
	ref, inputWasGloballyRoutableTrawlLinkForNotes, err := trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(
		ref,
		archive.AppID,
	)
	if err != nil {
		return "", err
	}
	if !trawlkit.ValidShortRef(ref) {
		return ref, nil
	}
	matches, err := req.ResolveShortReference(ctx, trawlkit.NewLocalTrawlerShortReference(ref))
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		if inputWasGloballyRoutableTrawlLinkForNotes {
			return "", noteLookupErrorForTrawlerCommand(archive.ErrNoteNotFound)
		}
		return ref, nil
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr("ambiguous_short_ref", "More than one note version has that link.", err)
	}
	if err != nil {
		return "", err
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(matches[0]), nil
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

func noteLookupErrorForTrawlerCommand(err error) error {
	switch {
	case errors.Is(err, archive.ErrNoteNotFound):
		return commandErr("not_found", "No note matched.", err)
	case errors.Is(err, archive.ErrNoteAmbiguous):
		return commandErr("ambiguous", "More than one note matched.", err)
	default:
		return err
	}
}

func noteLookupErrorForOpen(err error) error {
	if errors.Is(err, archive.ErrNoteAmbiguous) {
		return commandErr("invalid_input", "More than one note matched.", err)
	}
	return noteLookupErrorForTrawlerCommand(err)
}

// noteLabel names a note the way a human knows it: by title, never by the
// provider's note id.
func noteLabel(note archive.Note) string {
	if title := strings.TrimSpace(note.Title); title != "" {
		return title
	}
	return "(untitled note)"
}
