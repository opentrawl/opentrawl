package photos

import (
	"context"
	"flag"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/knownplacesfile"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const defaultPhotoListLimit = 20

type photoFieldListCommandFlags struct {
	on, after, before, place, person, album, camera string
	favourite                                       bool
	showAllPhotoDayStays                            bool
	limit                                           int
}

func (c *Crawler) registerPhotoFieldListCommandFlags(flagSet *flag.FlagSet) {
	c.photoListFlags = photoFieldListCommandFlags{limit: defaultPhotoListLimit}
	flagSet.StringVar(&c.photoListFlags.on, "on", "", "Photos on this capture-local `DATE` (YYYY-MM-DD)")
	c.registerPhotoDateRangeCommandFlags(flagSet)
	flagSet.StringVar(&c.photoListFlags.place, "place", "", "Match whole words in any Apple place level, address or area, or the place a card says the photo shows, by `NAME`")
	flagSet.StringVar(&c.photoListFlags.person, "person", "", "Match an Apple named person by `NAME`")
	flagSet.StringVar(&c.photoListFlags.album, "album", "", "Match an Apple album by `NAME`")
	flagSet.StringVar(&c.photoListFlags.camera, "camera", "", "Match camera make or model by `NAME`")
	flagSet.BoolVar(&c.photoListFlags.favourite, "favourite", false, "Only favourites")
}

func (c *Crawler) registerPhotoDateRangeCommandFlags(flagSet *flag.FlagSet) {
	flagSet.StringVar(&c.photoListFlags.after, "after", "", "First capture-local `DATE`, included (YYYY-MM-DD)")
	flagSet.StringVar(&c.photoListFlags.before, "before", "", "Last capture-local `DATE`, included (YYYY-MM-DD)")
	flagSet.IntVar(&c.photoListFlags.limit, "limit", defaultPhotoListLimit, "Maximum number of rows")
}

func (c *Crawler) registerPhotoDaysCommandFlags(flagSet *flag.FlagSet) {
	c.photoListFlags = photoFieldListCommandFlags{limit: defaultPhotoListLimit}
	c.registerPhotoDateRangeCommandFlags(flagSet)
	flagSet.BoolVar(&c.photoListFlags.showAllPhotoDayStays, "all-stays", false, "Show every stay in each day instead of the first twelve")
}

func photoCalendarDateOption(dateText, optionName string) (*presentation.CalendarDate, error) {
	if dateText == "" {
		return nil, nil
	}
	date, err := time.Parse("2006-01-02", dateText)
	if err != nil {
		return nil, render.CalendarDateOptionNeedsDate(optionName)
	}
	return &presentation.CalendarDate{CalendarYear: int32(date.Year()), CalendarMonthNumber: int32(date.Month()), CalendarDayOfMonth: int32(date.Day())}, nil
}

func (c *Crawler) photoFieldFilters() (photosarchive.PhotoFieldFilters, int, error) {
	listFlags := c.photoListFlags
	limit, err := flags.Limit(listFlags.limit, true)
	if err != nil {
		return photosarchive.PhotoFieldFilters{}, 0, err
	}
	if listFlags.on != "" && (listFlags.after != "" || listFlags.before != "") {
		return photosarchive.PhotoFieldFilters{}, 0, render.OptionsCannotBeUsedTogether("on", "after/before")
	}
	on, err := photoCalendarDateOption(listFlags.on, "on")
	if err != nil {
		return photosarchive.PhotoFieldFilters{}, 0, err
	}
	after, err := photoCalendarDateOption(listFlags.after, "after")
	if err != nil {
		return photosarchive.PhotoFieldFilters{}, 0, err
	}
	before, err := photoCalendarDateOption(listFlags.before, "before")
	if err != nil {
		return photosarchive.PhotoFieldFilters{}, 0, err
	}
	if listFlags.after != "" && listFlags.before != "" && listFlags.after > listFlags.before {
		return photosarchive.PhotoFieldFilters{}, 0, render.AfterMustNotBeLaterThanBefore()
	}
	return photosarchive.PhotoFieldFilters{On: on, From: after, To: before, Place: listFlags.place, Person: listFlags.person, Album: listFlags.album, Camera: listFlags.camera, Favourite: listFlags.favourite}, limit, nil
}

func (c *Crawler) listPhotoFactsCommand(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	filters, limit, err := c.photoFieldFilters()
	if err != nil {
		return nil, err
	}
	archive, err := c.readPhotoArchive(ctx, req)
	if err != nil {
		return nil, err
	}
	facts, total, err := archive.ListPhotoFacts(ctx, filters, limit)
	if err != nil {
		return nil, err
	}
	records := make([]*photo.PhotoRecord, 0, len(facts))
	for _, fact := range facts {
		photoCard, err := archive.ReadStoredPhotoCard(ctx, fact.GetApplePhotoUuid())
		if err != nil {
			return nil, err
		}
		record := &photo.PhotoRecord{CanonicalRecordReference: photoCanonicalRecordReference(fact), AppleLibraryPhotoFacts: fact}
		if photoCard.GetPhotoCardOutcome() == photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN {
			record.WrittenPhotoCardOneLineSummary = photoCard.GetOneLineSummary()
		}
		records = append(records, record)
	}
	return &command.TrawlerCommandResponse{TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PhotoListResponse{PhotoListResponse: &photo.PhotoListResponse{PhotoRecordsNewestFirst: records, PersonFilterIsApplied: filters.Person != "", TotalMatchingPhotoCount: total, MoreMatchingPhotosExist: uint64(len(records)) < total}}}, nil
}

func (c *Crawler) listPhotoLocalDaysCommand(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	filters, limit, err := c.photoFieldFilters()
	if err != nil {
		return nil, err
	}
	archive, err := c.readPhotoArchive(ctx, req)
	if err != nil {
		return nil, err
	}
	var knownPlaces *knownplaces.KnownPlacesFile
	if path := c.knownPlacesFilePath(); path != "" {
		knownPlaces, err = knownplacesfile.ReadKnownPlacesFile(config.ExpandHome(path))
		if err != nil {
			return nil, err
		}
	}
	days, err := archive.LocalPhotoDays(ctx, filters.From, filters.To, limit, knownPlaces)
	if err != nil {
		return nil, err
	}
	days.ShowAllPhotoDayStays = c.photoListFlags.showAllPhotoDayStays
	return &command.TrawlerCommandResponse{TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PhotoLocalDayListResponse{PhotoLocalDayListResponse: days}}, nil
}

func (c *Crawler) inspectPhotoLayerCommand(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	arguments := req.TrawlerCommandPositionalArguments
	if len(arguments) < 1 || len(arguments) > 2 {
		return nil, render.PhotosInspectNeedsPhoto()
	}
	layer := photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_APPLE_FACTS
	if len(arguments) == 2 {
		parsedLayer, found := photo.PhotoInspectionLayer_value["PHOTO_INSPECTION_LAYER_"+strings.ToUpper(strings.ReplaceAll(arguments[1], "-", "_"))]
		if !found || parsedLayer == 0 {
			return nil, render.PhotosInspectionLayerIsUnknown(arguments[1])
		}
		layer = photo.PhotoInspectionLayer(parsedLayer)
	}
	archive, err := c.readPhotoArchive(ctx, req)
	if err != nil {
		return nil, err
	}
	uuid, err := c.resolvePhotoArgument(ctx, req, arguments[0])
	if err != nil {
		return nil, err
	}
	needsCardModelInput := layer == photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_CARD ||
		layer == photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_CARD_MODEL_INPUT
	openedPhoto, currentModelInput, err := c.loadOpenedPhotoRecord(ctx, archive, req.TrawlerArchivePaths.TrawlerArchivePath, uuid, needsCardModelInput)
	if err != nil {
		return nil, err
	}
	inspectionResponse := &photo.PhotoInspectionResponse{PhotoRecord: openedPhoto, PhotoInspectionLayer: layer}
	switch layer {
	case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_NEARBY_PLACES:
		if err := addStoredNearbyPlacesFromCamera(ctx, archive, inspectionResponse); err != nil {
			return nil, err
		}
	case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_CARD_MODEL_INPUT:
		if currentModelInput == nil {
			if currentModelInput, err = c.currentPhotoCardModelInput(ctx, archive, uuid); err != nil {
				return nil, err
			}
		}
		inspectionResponse.CurrentPhotoCardModelInput = currentModelInput
	case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_UNSPECIFIED,
		photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_APPLE_FACTS,
		photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_PICTURE_METADATA,
		photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_CARD:
	}
	return &command.TrawlerCommandResponse{TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PhotoInspectionResponse{PhotoInspectionResponse: inspectionResponse}}, nil
}

func addStoredNearbyPlacesFromCamera(ctx context.Context, archive *photosarchive.PhotosArchive, inspectionResponse *photo.PhotoInspectionResponse) error {
	cameraLocation := inspectionResponse.GetPhotoRecord().GetAppleLibraryPhotoFacts().GetCameraLocation()
	if cameraLocation == nil {
		return nil
	}
	cameraSpot := photosarchive.CameraSpotForCameraPosition(cameraLocation.GetCameraLatitude(), cameraLocation.GetCameraLongitude())
	lookupResult, lookupIsStored, err := archive.ReadStoredNearbyPlacesLookupResult(ctx,
		placelookup.PhotonReverseLookupRequestForCameraSpot(cameraSpot.CameraSpotKey, cameraSpot.CameraSpotLatitude, cameraSpot.CameraSpotLongitude))
	if err != nil || !lookupIsStored {
		return err
	}
	inspectionResponse.NearbyPlacesLookupOutcome = lookupResult.GetPlaceLookupOutcome()
	inspectionResponse.NearbyPlacesLookupFailureMessage = lookupResult.GetPlaceLookupFailureMessage()
	inspectionResponse.NearbyPlacesFromCameraNearestFirst = placelookup.NearbyPlacesFromCameraNearestFirst(cameraLocation, lookupResult.GetNearbyPlacesNearestFirst())
	return nil
}
