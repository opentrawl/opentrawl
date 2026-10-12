package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type PhotoFieldFilters struct {
	On, From, To                 *presentation.CalendarDate
	Place, Person, Album, Camera string
	Favourite                    bool
}

func (photosArchive *PhotosArchive) ListPhotoFacts(ctx context.Context, filters PhotoFieldFilters, limit int) ([]*photo.AppleLibraryPhotoFacts, uint64, error) {
	photoUUIDs, total, err := photosArchive.matchingPhotoUUIDs(ctx, filters, limit)
	if err != nil {
		return nil, 0, err
	}
	facts, err := photosArchive.readPhotoFactsInBatches(ctx, photoUUIDs)
	return facts, total, err
}

func (photosArchive *PhotosArchive) matchingPhotoUUIDs(ctx context.Context, filters PhotoFieldFilters, limit int) ([]string, uint64, error) {
	where := "where absent_from_apple_library_since_time_utc is null"
	if filters.Favourite {
		where += " and is_favourite = 1"
	}
	selectedColumns := []string{"apple_photo_uuid", "capture_time_utc", "capture_utc_offset_seconds"}
	if filters.Place != "" {
		selectedColumns = append(selectedColumns, `coalesce(apple_stored_full_address,'') || char(10) || coalesce(apple_stored_iso_country_code,'') || char(10) ||
coalesce(apple_stored_country_name,'') || char(10) || coalesce(apple_stored_state_name,'') || char(10) || coalesce(apple_stored_county_name,'') || char(10) ||
coalesce(apple_stored_town_name,'') || char(10) || coalesce(apple_stored_neighbourhood_name,'') || char(10) || coalesce(apple_stored_street,'') || char(10) ||
coalesce(apple_stored_point_of_interest_name,'') || char(10) || coalesce(apple_stored_water_name,'') || char(10) || coalesce(apple_stored_island_name,'') || char(10) ||
coalesce((select group_concat(area_name,char(10)) from apple_library_photo_place_area a where a.apple_photo_uuid=p.apple_photo_uuid),'')`)
	}
	if filters.Person != "" {
		selectedColumns = append(selectedColumns, `coalesce((select group_concat(person_full_name,char(10)) from apple_library_photo_named_person n where n.apple_photo_uuid=p.apple_photo_uuid),'')`)
	}
	if filters.Album != "" {
		selectedColumns = append(selectedColumns, `coalesce((select group_concat(album_title,char(10)) from apple_library_photo_album a where a.apple_photo_uuid=p.apple_photo_uuid),'')`)
	}
	if filters.Camera != "" {
		selectedColumns = append(selectedColumns, `coalesce(camera_make,'') || ' ' || coalesce(camera_model,'') || ' ' || coalesce(lens_model,'')`)
	}
	rows, err := photosArchive.database.QueryContext(ctx, "select "+strings.Join(selectedColumns, ", ")+" from apple_library_photo p "+where+" order by capture_time_utc desc, apple_photo_uuid")
	if err != nil {
		return nil, 0, fmt.Errorf("select Apple photos by field: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var photoUUIDs []string
	var total uint64
	for rows.Next() {
		var uuid, captureUTC string
		var offset sql.NullInt32
		var placeNames, personNames, albumTitles, cameraNames string
		columns := []any{&uuid, &captureUTC, &offset}
		if filters.Place != "" {
			columns = append(columns, &placeNames)
		}
		if filters.Person != "" {
			columns = append(columns, &personNames)
		}
		if filters.Album != "" {
			columns = append(columns, &albumTitles)
		}
		if filters.Camera != "" {
			columns = append(columns, &cameraNames)
		}
		if err := rows.Scan(columns...); err != nil {
			return nil, 0, fmt.Errorf("read photo field selection: %w", err)
		}
		if !containsWholePhotoPlaceNameIgnoringCase(placeNames, filters.Place) || !containsPhotoFieldIgnoringCase(personNames, filters.Person) || !containsPhotoFieldIgnoringCase(albumTitles, filters.Album) || !containsPhotoFieldIgnoringCase(cameraNames, filters.Camera) {
			continue
		}
		if filters.On != nil || filters.From != nil || filters.To != nil {
			captureTime, err := parseArchiveTime(captureUTC)
			if err != nil {
				return nil, 0, err
			}
			captureLocalTime := &photo.PhotoCaptureLocalTime{CaptureTimeUtc: timestamppb.New(captureTime), CaptureUtcOffsetSeconds: optionalInt32FromColumn(offset)}
			if captureLocalTime.CaptureUtcOffsetSeconds == nil {
				continue
			}
			localDate := photoLocalCalendarDate(captureLocalTime)
			if !photoLocalDateMatches(localDate, filters.On, filters.From, filters.To) {
				continue
			}
		}
		total++
		if limit <= 0 || len(photoUUIDs) < limit {
			photoUUIDs = append(photoUUIDs, uuid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("select Apple photos by field: %w", err)
	}
	return photoUUIDs, total, nil
}

func photoLocalCalendarDate(captureLocalTime *photo.PhotoCaptureLocalTime) *presentation.CalendarDate {
	localTime := captureLocalTime.GetCaptureTimeUtc().AsTime().Add(time.Duration(captureLocalTime.GetCaptureUtcOffsetSeconds()) * time.Second)
	return &presentation.CalendarDate{CalendarYear: int32(localTime.Year()), CalendarMonthNumber: int32(localTime.Month()), CalendarDayOfMonth: int32(localTime.Day())}
}

func photoCalendarDateNumber(date *presentation.CalendarDate) int32 {
	return date.GetCalendarYear()*10000 + date.GetCalendarMonthNumber()*100 + date.GetCalendarDayOfMonth()
}

func photoLocalDateMatches(date, on, from, to *presentation.CalendarDate) bool {
	number := photoCalendarDateNumber(date)
	return (on == nil || number == photoCalendarDateNumber(on)) && (from == nil || number >= photoCalendarDateNumber(from)) && (to == nil || number <= photoCalendarDateNumber(to))
}

func (photosArchive *PhotosArchive) readPhotoFactsInBatches(ctx context.Context, photoUUIDs []string) ([]*photo.AppleLibraryPhotoFacts, error) {
	var facts []*photo.AppleLibraryPhotoFacts
	for first := 0; first < len(photoUUIDs); first += 500 {
		batch, err := photosArchive.ReadStoredAppleLibraryPhotoFacts(ctx, photoUUIDs[first:min(first+500, len(photoUUIDs))])
		if err != nil {
			return nil, err
		}
		facts = append(facts, batch...)
	}
	return facts, nil
}

func containsWholePhotoPlaceNameIgnoringCase(placeNames, wantedPlaceName string) bool {
	wantedPlaceName = strings.TrimSpace(wantedPlaceName)
	if wantedPlaceName == "" {
		return true
	}
	wantedPlaceRunes := []rune(wantedPlaceName)
	for _, placeName := range strings.Split(placeNames, "\n") {
		placeNameRunes := []rune(placeName)
		for start := 0; start+len(wantedPlaceRunes) <= len(placeNameRunes); start++ {
			end := start + len(wantedPlaceRunes)
			if !strings.EqualFold(string(placeNameRunes[start:end]), wantedPlaceName) {
				continue
			}
			if start > 0 && photoPlaceNameRuneIsPartOfWord(placeNameRunes[start-1]) {
				continue
			}
			if end < len(placeNameRunes) && photoPlaceNameRuneIsPartOfWord(placeNameRunes[end]) {
				continue
			}
			return true
		}
	}
	return false
}

func photoPlaceNameRuneIsPartOfWord(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsNumber(character) || unicode.IsMark(character)
}
