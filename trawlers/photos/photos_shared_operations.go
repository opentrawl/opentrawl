package photos

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/knownplacesfile"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photocard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photospipeline"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturecache"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturefetch"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

var (
	_ trawlkit.Searcher     = (*Crawler)(nil)
	_ trawlkit.RecordOpener = (*Crawler)(nil)
)

func photoCanonicalRecordReference(facts *photo.AppleLibraryPhotoFacts) *trawlkit.CanonicalArchiveRecordReference {
	return trawlkit.NewCanonicalArchiveRecordReference("photos:" + facts.GetApplePhotoUuid())
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	archive, err := c.readPhotoArchive(ctx, req)
	if err != nil {
		return nil, err
	}
	matches, total, err := archive.SearchPhotos(ctx, query)
	if err != nil {
		return nil, err
	}
	response := &search.TrawlerSearchResponse{TotalSearchMatches: total, MoreSearchMatchesExist: uint64(len(matches)) < total}
	for _, match := range matches {
		facts := match.AppleLibraryPhotoFacts
		captureTime := facts.GetCaptureLocalTime()
		offset := captureTime.CaptureUtcOffsetSeconds
		if offset == nil {
			offset = new(int32(0))
		}
		matchPresentation := &search.SearchMatchPresentation{
			MatchingRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay{ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: captureTime.GetCaptureTimeUtc()}, EventUtcOffsetSecondsRecordedBySourceArchive: offset},
			MatchingRecordDisplayName:    match.WrittenPhotoCard.GetOneLineSummary(), MatchingRecordKindDisplayName: "photo",
			SearchMatchTextFieldsInDisplayOrder: match.SearchMatchTextFieldsInDisplayOrder,
			AppleLibraryPhotoFacts:              facts,
		}
		if matchPresentation.MatchingRecordDisplayName == "" {
			matchPresentation.MatchingRecordDisplayName = facts.GetAppleTitle()
		}
		if matchPresentation.MatchingRecordDisplayName == "" {
			matchPresentation.MatchingRecordDisplayName = facts.GetAppleCaption()
		}
		if matchPresentation.MatchingRecordDisplayName == "" {
			matchPresentation.MatchingRecordDisplayName = render.PhotoMediaKindDisplayName(facts)
		}
		for _, namedPerson := range facts.GetAppleNamedPeople() {
			matchPresentation.PeopleRelatedToMatchingRecord = append(matchPresentation.PeopleRelatedToMatchingRecord, &person.PersonRelatedToArchiveRecord{PersonDisplayName: namedPerson.GetPersonFullName()})
		}
		response.TrawlerSearchMatchesInDisplayOrder = append(response.TrawlerSearchMatchesInDisplayOrder, &search.TrawlerSearchMatch{CanonicalRecordReference: photoCanonicalRecordReference(facts), RecordAnchor: trawlkit.NewRecordAnchorIdentifier(openrecord.PhotoDetailAnchorID), SearchMatchPresentation: matchPresentation})
	}
	return response, nil
}

// loadOpenedPhotoRecord reads one photo's facts, picture and card. When
// checkCardModelInputChanged is true, it renders the photo's card model input
// from today's facts to say whether the card was written from older facts, and
// returns that rendered input so callers do not render it twice.
func (c *Crawler) loadOpenedPhotoRecord(ctx context.Context, archive *photosarchive.PhotosArchive, photosArchivePath, uuid string, checkCardModelInputChanged bool) (*photo.OpenedPhotoRecord, *photo.PhotoCardModelInput, error) {
	facts, err := archive.ReadStoredAppleLibraryPhotoFacts(ctx, []string{uuid})
	if err != nil {
		return nil, nil, err
	}
	if len(facts) == 0 {
		return nil, nil, render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPhoto)
	}
	trips, err := archive.ReadTripsForPhotoFacts(ctx, facts)
	if err != nil {
		return nil, nil, err
	}
	pictureFetchResult, err := archive.ReadStoredPictureFetchResult(ctx, uuid)
	if err != nil {
		return nil, nil, err
	}
	photoCard, err := archive.ReadStoredPhotoCard(ctx, uuid)
	if err != nil {
		return nil, nil, err
	}
	openedPhoto := &photo.OpenedPhotoRecord{
		CanonicalRecordReference: photoCanonicalRecordReference(facts[0]),
		AppleLibraryPhotoFacts:   facts[0], AppleTrips: trips,
		PictureFetchResult: pictureFetchResult,
		PhotoCard:          photoCard,
	}
	if pictureFetchResult.GetPictureSha256() != "" {
		openedPhoto.CachedPictureFilePath, _ = picturecache.NewPictureCache(picturecache.PictureCacheFolderPath(photosArchivePath)).CachedPictureFilePath(pictureFetchResult.GetPictureSha256())
	}
	var currentModelInput *photo.PhotoCardModelInput
	if checkCardModelInputChanged && photoCard.GetPhotoCardOutcome() == photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN {
		currentModelInput, err = c.currentPhotoCardModelInput(ctx, archive, uuid)
		if err != nil {
			return nil, nil, err
		}
		openedPhoto.CardModelInputChangedSinceCardWasWritten = currentModelInput.GetPhotoCardModelInputSha256() != photoCard.GetPhotoCardModelInputSha256Used()
	}
	return openedPhoto, currentModelInput, nil
}

// currentPhotoCardModelInput renders what the card model would read for the
// photo now.
func (c *Crawler) currentPhotoCardModelInput(ctx context.Context, archive *photosarchive.PhotosArchive, uuid string) (*photo.PhotoCardModelInput, error) {
	var knownPlacesFile *knownplaces.KnownPlacesFile
	if path := c.knownPlacesFilePath(); path != "" {
		var err error
		if knownPlacesFile, err = knownplacesfile.ReadKnownPlacesFile(config.ExpandHome(path)); err != nil {
			return nil, err
		}
	}
	sources, photoIsStored, err := photospipeline.ReadPhotoCardModelInputSources(ctx, archive, uuid, knownPlacesFile)
	if err != nil || !photoIsStored {
		return nil, err
	}
	return photocard.RenderPhotoCardModelInput(sources)
}

func (c *Crawler) OpenRecord(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, localReference *trawlkit.LocalTrawlerShortReference) (*open.OpenRecord, error) {
	archive, err := c.readPhotoArchive(ctx, req)
	if err != nil {
		return nil, err
	}
	uuid, err := c.resolvePhotoArgument(ctx, req, trawlkit.LocalTrawlerShortReferenceText(localReference))
	if err != nil {
		return nil, err
	}
	openedPhoto, _, err := c.loadOpenedPhotoRecord(ctx, archive, req.TrawlerArchivePaths.TrawlerArchivePath, strings.TrimPrefix(uuid, "photos:"), true)
	if err != nil {
		return nil, err
	}
	if !trawlkit.ShouldSkipOnDemandPhotoPictureHydration(ctx) {
		c.hydrateOpenedPhotoPictureIntoCache(ctx, req.TrawlerArchivePaths.TrawlerArchivePath, openedPhoto)
	}
	photoReferences, err := req.LocalShortReferencesForCanonicalArchiveRecordReferences(ctx, []*trawlkit.CanonicalArchiveRecordReference{openedPhoto.GetCanonicalRecordReference()})
	if err != nil {
		return nil, err
	}
	openedPhoto.PhotoTrawlLink, err = trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
		RegisteredTrawler:   c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		LocalShortReference: trawlkit.LocalTrawlerShortReferenceForCanonicalArchiveRecordReference(photoReferences, openedPhoto.GetCanonicalRecordReference()),
	})
	if err != nil {
		return nil, err
	}
	record := &open.OpenRecord{RecordTrawler: c.RegisteredTrawlerDeclaration().RegisteredTrawler, CanonicalRecordReference: openedPhoto.GetCanonicalRecordReference(), TypedOpenedRecord: &open.OpenRecord_OpenedPhotoRecord{OpenedPhotoRecord: openedPhoto}}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

const onDemandPictureFetchPersistTimeout = 5 * time.Second

type onDemandPictureFetchKey struct {
	photosArchivePath string
	applePhotoUUID    string
}

type inFlightOnDemandPictureFetch struct {
	done   chan struct{}
	result *photo.PictureFetchResult
	err    error
}

// hydrateOpenedPhotoPictureIntoCache restores an evicted or unfetched picture
// into the local picture cache when a photo is opened, persists a newly fetched
// result into photos.db so future opens hit the picture cache immediately, and
// deduplicates concurrent fetches for the same Apple photo UUID. A missing
// mirror folder falls back to Apple Photos for this single photo; any fetch
// failure is recorded on openedPhoto.PictureFetchResult so callers can show an
// honest reason while leaving the text record intact.
func (c *Crawler) hydrateOpenedPhotoPictureIntoCache(ctx context.Context, photosArchivePath string, openedPhoto *photo.OpenedPhotoRecord) {
	if openedPhoto == nil || openedPhoto.GetCachedPictureFilePath() != "" {
		return
	}
	facts := openedPhoto.GetAppleLibraryPhotoFacts()
	if facts == nil {
		return
	}
	fetchStep := c.fetchPictureStep(photosArchivePath, true)
	cachedPicturePath := func(pictureSHA256 string) (string, bool) {
		return fetchStep.PictureCache.CachedPictureFilePath(pictureSHA256)
	}
	fetchAndPersistOnce := func(fetchCtx context.Context, request *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
		var fetchedResult *photo.PictureFetchResult
		var err error
		if c.onDemandPictureFetchOverrideFunc != nil {
			fetchedResult, err = c.onDemandPictureFetchOverrideFunc(fetchCtx, photosArchivePath, request)
		} else {
			fetchedResult, err = fetchStep.RunPhotosStepOnOneInput(fetchCtx, request)
			var mirrorMissing *picturefetch.PictureMirrorFolderIsMissingError
			if errors.As(err, &mirrorMissing) {
				fetchStep.PictureMirrorFolder = nil
				fetchedResult, err = fetchStep.RunPhotosStepOnOneInput(fetchCtx, request)
			}
		}
		if err == nil &&
			fetchedResult.GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED {
			if _, ok := cachedPicturePath(fetchedResult.GetPictureSha256()); ok {
				_ = c.persistOnDemandPictureFetchResult(fetchCtx, photosArchivePath, fetchedResult)
			}
		}
		return fetchedResult, err
	}
	hydrateOpenedPhotoPicture(
		ctx,
		openedPhoto,
		func(callCtx context.Context, request *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
			return c.runDeduplicatedOnDemandPictureFetch(callCtx, photosArchivePath, request.GetApplePhotoUuid(), func(leaderCtx context.Context) (*photo.PictureFetchResult, error) {
				return fetchAndPersistOnce(leaderCtx, request)
			})
		},
		cachedPicturePath,
		nil,
	)
}

func (c *Crawler) runDeduplicatedOnDemandPictureFetch(
	ctx context.Context,
	photosArchivePath string,
	applePhotoUUID string,
	fetchPicture func(context.Context) (*photo.PictureFetchResult, error),
) (*photo.PictureFetchResult, error) {
	key := onDemandPictureFetchKey{
		photosArchivePath: photosArchivePath,
		applePhotoUUID:    applePhotoUUID,
	}
	c.onDemandPictureFetchesMu.Lock()
	if c.inFlightOnDemandPictureFetches == nil {
		c.inFlightOnDemandPictureFetches = make(map[onDemandPictureFetchKey]*inFlightOnDemandPictureFetch)
	}
	call, alreadyInFlight := c.inFlightOnDemandPictureFetches[key]
	if !alreadyInFlight {
		call = &inFlightOnDemandPictureFetch{done: make(chan struct{})}
		c.inFlightOnDemandPictureFetches[key] = call
		go func(leaderCtx context.Context) {
			defer func() {
				c.onDemandPictureFetchesMu.Lock()
				delete(c.inFlightOnDemandPictureFetches, key)
				c.onDemandPictureFetchesMu.Unlock()
				close(call.done)
			}()
			call.result, call.err = fetchPicture(leaderCtx)
		}(context.WithoutCancel(ctx))
	}
	c.onDemandPictureFetchesMu.Unlock()

	select {
	case <-call.done:
		if call.result == nil {
			return nil, call.err
		}
		return proto.Clone(call.result).(*photo.PictureFetchResult), call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Crawler) persistOnDemandPictureFetchResult(
	ctx context.Context,
	photosArchivePath string,
	fetchedResult *photo.PictureFetchResult,
) error {
	trimmedArchivePath := strings.TrimSpace(photosArchivePath)
	if trimmedArchivePath == "" || fetchedResult == nil {
		return nil
	}
	c.onDemandPictureFetchPersistMu.Lock()
	defer c.onDemandPictureFetchPersistMu.Unlock()

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), onDemandPictureFetchPersistTimeout)
	defer cancel()

	writableStore, err := store.Open(writeCtx, store.Options{Path: trimmedArchivePath})
	if err != nil {
		return err
	}
	defer func() { _ = writableStore.Close() }()

	writableArchive, exists, err := photosarchive.UseExistingPhotosArchive(writeCtx, writableStore)
	if err != nil || !exists {
		return err
	}
	return writableArchive.StorePictureFetchResult(writeCtx, fetchedResult, time.Now().UTC())
}

func hydrateOpenedPhotoPicture(
	ctx context.Context,
	openedPhoto *photo.OpenedPhotoRecord,
	fetchPicture func(context.Context, *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error),
	cachedPicturePath func(pictureSHA256 string) (string, bool),
	persistPictureFetchResult func(context.Context, *photo.PictureFetchResult) error,
) {
	if openedPhoto == nil || openedPhoto.GetCachedPictureFilePath() != "" {
		return
	}
	facts := openedPhoto.GetAppleLibraryPhotoFacts()
	if facts == nil {
		return
	}
	fetchRequest := &photosstep.FetchPictureRequest{
		ApplePhotoUuid:         facts.GetApplePhotoUuid(),
		CaptureTimeUtc:         facts.GetCaptureLocalTime().GetCaptureTimeUtc(),
		AppleMediaKind:         facts.GetAppleMediaKind(),
		AppleAdjustmentsState:  facts.GetAppleAdjustmentsState(),
		AppleAdjustmentTimeUtc: facts.GetAppleAdjustmentTimeUtc(),
	}
	fetchedResult, err := fetchPicture(ctx, fetchRequest)
	if err != nil {
		openedPhoto.PictureFetchResult = failedOnDemandPictureFetchResult(facts, err)
		return
	}
	if fetchedResult.GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED {
		if cachedPath, ok := cachedPicturePath(fetchedResult.GetPictureSha256()); ok {
			openedPhoto.CachedPictureFilePath = cachedPath
			openedPhoto.PictureFetchResult = fetchedResult
			if persistPictureFetchResult != nil {
				_ = persistPictureFetchResult(ctx, fetchedResult)
			}
			return
		}
		openedPhoto.PictureFetchResult = &photo.PictureFetchResult{
			ApplePhotoUuid:                   facts.GetApplePhotoUuid(),
			FetchedForAppleAdjustmentsState:  facts.GetAppleAdjustmentsState(),
			FetchedForAppleAdjustmentTimeUtc: facts.GetAppleAdjustmentTimeUtc(),
			PictureFetchOutcome:              photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
			PictureFetchFailureMessage:       "The fetched picture could not be found in the local picture cache.",
			PictureSource:                    fetchedResult.GetPictureSource(),
		}
		return
	}
	if fetchedResult != nil && fetchedResult.GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED {
		preserved := &photo.PictureFetchResult{
			ApplePhotoUuid:                   fetchedResult.GetApplePhotoUuid(),
			FetchedForAppleAdjustmentsState:  fetchedResult.GetFetchedForAppleAdjustmentsState(),
			FetchedForAppleAdjustmentTimeUtc: fetchedResult.GetFetchedForAppleAdjustmentTimeUtc(),
			PictureFetchOutcome:              photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
			PictureFetchFailureMessage:       safeOnDemandPictureFetchFailureMessage(fetchedResult.GetPictureFetchFailureMessage()),
			RetryNotBeforeTimeUtc:            fetchedResult.GetRetryNotBeforeTimeUtc(),
			PictureSource:                    fetchedResult.GetPictureSource(),
			PictureLongEdgePixelLimitUsed:    fetchedResult.PictureLongEdgePixelLimitUsed,
		}
		if preserved.ApplePhotoUuid == "" {
			preserved.ApplePhotoUuid = facts.GetApplePhotoUuid()
		}
		openedPhoto.PictureFetchResult = preserved
		return
	}
	openedPhoto.PictureFetchResult = &photo.PictureFetchResult{
		ApplePhotoUuid:                   facts.GetApplePhotoUuid(),
		FetchedForAppleAdjustmentsState:  facts.GetAppleAdjustmentsState(),
		FetchedForAppleAdjustmentTimeUtc: facts.GetAppleAdjustmentTimeUtc(),
		PictureFetchOutcome:              photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
		PictureFetchFailureMessage:       "The picture could not be fetched.",
		PictureSource:                    photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS,
	}
}

func failedOnDemandPictureFetchResult(facts *photo.AppleLibraryPhotoFacts, fetchErr error) *photo.PictureFetchResult {
	failureMessage := "OpenTrawl could not read or cache the picture."
	pictureSource := photo.PictureSource_PICTURE_SOURCE_UNSPECIFIED
	switch {
	case errors.Is(fetchErr, picturefetch.ErrApplePhotosAccessNotGranted):
		failureMessage = "OpenTrawl does not have full access to Apple Photos. Allow OpenTrawl in System Settings → Privacy & Security → Photos."
		pictureSource = photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS
	case errors.Is(fetchErr, context.DeadlineExceeded):
		failureMessage = "Fetching the picture timed out."
	case errors.Is(fetchErr, context.Canceled):
		failureMessage = "Fetching the picture was cancelled."
	}
	return &photo.PictureFetchResult{
		ApplePhotoUuid:                   facts.GetApplePhotoUuid(),
		FetchedForAppleAdjustmentsState:  facts.GetAppleAdjustmentsState(),
		FetchedForAppleAdjustmentTimeUtc: facts.GetAppleAdjustmentTimeUtc(),
		PictureFetchOutcome:              photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
		PictureFetchFailureMessage:       failureMessage,
		PictureSource:                    pictureSource,
	}
}

func safeOnDemandPictureFetchFailureMessage(rawMessage string) string {
	trimmed := strings.TrimSpace(rawMessage)
	if trimmed == "" {
		return "Apple Photos could not deliver the picture."
	}
	lower := strings.ToLower(trimmed)
	for _, pathPrefix := range []string{"/users/", "/volumes/", "/private/", "/var/", "/tmp/", "/library/"} {
		if strings.Contains(lower, pathPrefix) {
			return "The picture file could not be read on the archive host."
		}
	}
	return trimmed
}
