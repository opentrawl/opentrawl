package photos

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photospipeline"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
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
	matches, total, err := archive.SearchPhotoFacts(ctx, query)
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
			MatchingRecordDisplayName:    facts.GetAppleTitle(), MatchingRecordKindDisplayName: "photo",
			SearchMatchTextFieldsInDisplayOrder: match.SearchMatchTextFieldsInDisplayOrder,
			AppleLibraryPhotoFacts:              facts,
		}
		if matchPresentation.MatchingRecordDisplayName == "" {
			matchPresentation.MatchingRecordDisplayName = facts.GetAppleCaption()
		}
		for _, namedPerson := range facts.GetAppleNamedPeople() {
			matchPresentation.PeopleRelatedToMatchingRecord = append(matchPresentation.PeopleRelatedToMatchingRecord, &person.PersonRelatedToArchiveRecord{PersonDisplayName: namedPerson.GetPersonFullName()})
		}
		response.TrawlerSearchMatchesInDisplayOrder = append(response.TrawlerSearchMatchesInDisplayOrder, &search.TrawlerSearchMatch{CanonicalRecordReference: photoCanonicalRecordReference(facts), RecordAnchor: trawlkit.NewRecordAnchorIdentifier(openrecord.PhotoDetailAnchorID), SearchMatchPresentation: matchPresentation})
	}
	return response, nil
}

func loadOpenedPhotoRecord(ctx context.Context, archive *photosarchive.PhotosArchive, uuid string) (*photo.OpenedPhotoRecord, error) {
	facts, err := archive.ReadStoredAppleLibraryPhotoFacts(ctx, []string{uuid})
	if err != nil {
		return nil, err
	}
	if len(facts) == 0 {
		return nil, render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPhoto)
	}
	trips, err := archive.ReadTripsForPhotoFacts(ctx, facts)
	if err != nil {
		return nil, err
	}
	pictureFetchResult, err := archive.ReadStoredPictureFetchResult(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return &photo.OpenedPhotoRecord{
		CanonicalRecordReference: photoCanonicalRecordReference(facts[0]),
		AppleLibraryPhotoFacts:   facts[0], AppleTrips: trips,
		PictureFetchResult:      pictureFetchResult,
		PictureFetchStepIsBuilt: photospipeline.PhotosStepIsBuilt(photosstep.PhotosStepName_PHOTOS_STEP_NAME_FETCH_PICTURE),
	}, nil
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
	openedPhoto, err := loadOpenedPhotoRecord(ctx, archive, strings.TrimPrefix(uuid, "photos:"))
	if err != nil {
		return nil, err
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
