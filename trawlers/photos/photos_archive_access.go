package photos

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type archiveFileSnapshotFingerprint struct {
	exists          bool
	byteSize        int64
	modTimeUnixNano int64
}

type photoArchiveShortReferenceCatalogCacheKey struct {
	photosArchivePath string
	databaseFile      archiveFileSnapshotFingerprint
	walFile           archiveFileSnapshotFingerprint
}

type cachedPhotoReadOnlyShortReferenceCatalog struct {
	cacheKey photoArchiveShortReferenceCatalogCacheKey
	entries  []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference
	valid    bool
}

func snapshotArchiveFileFingerprint(filePath string) archiveFileSnapshotFingerprint {
	if strings.TrimSpace(filePath) == "" {
		return archiveFileSnapshotFingerprint{}
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return archiveFileSnapshotFingerprint{}
	}
	return archiveFileSnapshotFingerprint{
		exists:          true,
		byteSize:        info.Size(),
		modTimeUnixNano: info.ModTime().UnixNano(),
	}
}

func photoShortReferenceCatalogCacheKey(photosArchivePath string) (photoArchiveShortReferenceCatalogCacheKey, bool) {
	trimmedPath := strings.TrimSpace(photosArchivePath)
	if trimmedPath == "" || trimmedPath == ":memory:" || strings.HasPrefix(trimmedPath, "file:") {
		return photoArchiveShortReferenceCatalogCacheKey{}, false
	}
	dbFingerprint := snapshotArchiveFileFingerprint(trimmedPath)
	if !dbFingerprint.exists {
		return photoArchiveShortReferenceCatalogCacheKey{}, false
	}
	return photoArchiveShortReferenceCatalogCacheKey{
		photosArchivePath: trimmedPath,
		databaseFile:      dbFingerprint,
		walFile:           snapshotArchiveFileFingerprint(trimmedPath + "-wal"),
	}, true
}

func (c *Crawler) readPhotoArchive(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*photosarchive.PhotosArchive, error) {
	archive, exists, err := photosarchive.UseExistingPhotosArchive(ctx, req.OpenedTrawlerArchiveStore)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, trawlkit.NewMissingArchiveError(req.TrawlerArchivePaths.TrawlerArchivePath)
	}
	if req.HasReadOnlyShortReferenceCatalog() {
		return archive, nil
	}
	cacheKey, cacheable := photoShortReferenceCatalogCacheKey(req.TrawlerArchivePaths.TrawlerArchivePath)
	if cacheable {
		c.readOnlyShortReferenceCatalogMu.Lock()
		cached := c.cachedReadOnlyShortReferenceCatalog
		c.readOnlyShortReferenceCatalogMu.Unlock()
		if cached.valid && cached.cacheKey == cacheKey {
			req.SetPrecomputedReadOnlyShortReferenceCatalog(cached.entries)
			return archive, nil
		}
	}
	references, err := archive.ReadPresentPhotoCanonicalRecordReferences(ctx)
	if err != nil {
		return nil, err
	}
	catalog, err := trawlkit.BuildReadOnlyShortReferenceCatalog(references)
	if err != nil {
		return nil, err
	}
	if cacheable {
		c.readOnlyShortReferenceCatalogMu.Lock()
		c.cachedReadOnlyShortReferenceCatalog = cachedPhotoReadOnlyShortReferenceCatalog{
			cacheKey: cacheKey,
			entries:  catalog,
			valid:    true,
		}
		c.readOnlyShortReferenceCatalogMu.Unlock()
	}
	req.SetPrecomputedReadOnlyShortReferenceCatalog(catalog)
	return archive, nil
}

var applePhotoUUIDArgumentPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (c *Crawler) resolvePhotoArgument(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, argument string) (string, error) {
	localReference, wasLink, err := trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(argument, "photos")
	if err != nil {
		return "", err
	}
	if wasLink || trawlkit.ValidShortRef(localReference) {
		if !req.HasReadOnlyShortReferenceCatalog() {
			if _, err := c.readPhotoArchive(ctx, req); err != nil {
				return "", err
			}
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
