package photos

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) readPhotoArchive(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*photosarchive.PhotosArchive, error) {
	archive, exists, err := photosarchive.UseExistingPhotosArchive(ctx, req.OpenedTrawlerArchiveStore)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, trawlkit.NewMissingArchiveError(req.TrawlerArchivePaths.TrawlerArchivePath)
	}
	references, err := archive.ReadPresentPhotoCanonicalRecordReferences(ctx)
	if err != nil {
		return nil, err
	}
	if err := req.SetReadOnlyShortReferenceCatalog(references); err != nil {
		return nil, err
	}
	return archive, nil
}

var applePhotoUUIDArgumentPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (c *Crawler) resolvePhotoArgument(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, argument string) (string, error) {
	localReference, wasLink, err := trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(argument, "photos")
	if err != nil {
		return "", err
	}
	if wasLink || trawlkit.ValidShortRef(localReference) {
		_, err := c.readPhotoArchive(ctx, req)
		if err != nil {
			return "", err
		}
		references, err := req.ResolveShortReference(ctx, trawlkit.NewLocalTrawlerShortReference(localReference))
		if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
			return "", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounPhoto)
		}
		if errors.Is(err, trawlkit.ErrUnknownShortRef) {
			return "", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPhoto)
		}
		if err != nil {
			return "", err
		}
		return strings.TrimPrefix(trawlkit.CanonicalArchiveRecordReferenceText(references[0]), "photos:"), nil
	}
	applePhotoUUIDArgument := strings.TrimPrefix(localReference, "photos:")
	if applePhotoUUIDArgumentPattern.MatchString(applePhotoUUIDArgument) {
		return strings.ToUpper(applePhotoUUIDArgument), nil
	}
	return localReference, nil
}
