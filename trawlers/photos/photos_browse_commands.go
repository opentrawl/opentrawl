package photos

import (
	"context"
	"flag"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/knownplacesfile"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const defaultPhotoListLimit = 20

type photoFieldListCommandFlags struct {
	on, from, to, place, person, album, camera string
	favourite                                  bool
	showAllPhotoDayStays                       bool
	limit                                      int
}

func (c *Crawler) registerPhotoFieldListCommandFlags(flagSet *flag.FlagSet) {
	c.photoListFlags = photoFieldListCommandFlags{limit: defaultPhotoListLimit}
	flagSet.StringVar(&c.photoListFlags.on, "on", "", "Photos on this capture-local `DATE` (YYYY-MM-DD)")
	c.registerPhotoDateRangeCommandFlags(flagSet)
	flagSet.StringVar(&c.photoListFlags.place, "place", "", "Match whole words in any Apple place level, address or area by `NAME`")
	flagSet.StringVar(&c.photoListFlags.person, "person", "", "Match an Apple named person by `NAME`")
	flagSet.StringVar(&c.photoListFlags.album, "album", "", "Match an Apple album by `NAME`")
	flagSet.StringVar(&c.photoListFlags.camera, "camera", "", "Match camera make or model by `NAME`")
	flagSet.BoolVar(&c.photoListFlags.favourite, "favourite", false, "Only favourites")
}

func (c *Crawler) registerPhotoDateRangeCommandFlags(flagSet *flag.FlagSet) {
	flagSet.StringVar(&c.photoListFlags.from, "from", "", "First capture-local `DATE`, included (YYYY-MM-DD)")
	flagSet.StringVar(&c.photoListFlags.to, "to", "", "Last capture-local `DATE`, included (YYYY-MM-DD)")
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
	if listFlags.on != "" && (listFlags.from != "" || listFlags.to != "") {
		return photosarchive.PhotoFieldFilters{}, 0, render.OptionsCannotBeUsedTogether("on", "from/to")
	}
	on, err := photoCalendarDateOption(listFlags.on, "on")
	if err != nil {
		return photosarchive.PhotoFieldFilters{}, 0, err
	}
	from, err := photoCalendarDateOption(listFlags.from, "from")
	if err != nil {
		return photosarchive.PhotoFieldFilters{}, 0, err
	}
	to, err := photoCalendarDateOption(listFlags.to, "to")
	if err != nil {
		return photosarchive.PhotoFieldFilters{}, 0, err
	}
	if listFlags.from != "" && listFlags.to != "" && listFlags.from > listFlags.to {
		return photosarchive.PhotoFieldFilters{}, 0, render.FromDateMustNotBeLaterThanToDate()
	}
	return photosarchive.PhotoFieldFilters{On: on, From: from, To: to, Place: listFlags.place, Person: listFlags.person, Album: listFlags.album, Camera: listFlags.camera, Favourite: listFlags.favourite}, limit, nil
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
		records = append(records, &photo.PhotoRecord{CanonicalRecordReference: photoCanonicalRecordReference(fact), AppleLibraryPhotoFacts: fact})
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
		knownPlaces, err = knownplacesfile.ReadKnownPlacesFile(path)
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
	openedPhoto, err := loadOpenedPhotoRecord(ctx, archive, uuid)
	if err != nil {
		return nil, err
	}
	return &command.TrawlerCommandResponse{TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PhotoInspectionResponse{PhotoInspectionResponse: &photo.PhotoInspectionResponse{PhotoRecord: openedPhoto, PhotoInspectionLayer: layer}}}, nil
}
