package render

import (
	"fmt"
	"io"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

const maximumPhotoDayStaysForHumanOutput = 12

func photoDayStayHeadingForHumanOutput(stay *photo.PhotoDayStay) string {
	place := stay.GetRepresentativeAppleStoredPlace()
	administrativeNames := nonEmptySearchResultNames([]string{place.GetAppleStoredTownName(), place.GetAppleStoredStateName(), place.GetAppleStoredCountryName()})
	heading := strings.Join(administrativeNames, ", ")
	if water := place.GetAppleStoredWaterName(); water != "" && strings.EqualFold(water, stay.GetMostSharedApplePlaceName()) {
		heading = "Over water"
	}
	if heading == "" {
		heading = "Local times"
	}
	return heading + " · " + photoDayStayUTCOffsetForHumanOutput(stay.GetFirstPhotoCaptureLocalTime())
}

func photoDayStayUTCOffsetForHumanOutput(capture *photo.PhotoCaptureLocalTime) string {
	offset := capture.GetCaptureUtcOffsetSeconds()
	eventTime, _ := archiveEventTimeFromTimestamp(capture.GetCaptureTimeUtc(), &offset)
	return eventTime.localTime().Format(utcOffsetLayout)
}

func photoDayStayTimeForHumanOutput(capture *photo.PhotoCaptureLocalTime, localDate *presentation.CalendarDate) string {
	offset := capture.GetCaptureUtcOffsetSeconds()
	eventTime, valid := archiveEventTimeFromTimestamp(capture.GetCaptureTimeUtc(), &offset)
	if !valid {
		return ""
	}
	localTime := eventTime.localTime()
	if localTime.Format(calendarDateLayout) != formatCalendarDate(localDate) {
		return localTime.Format(calendarDateLayout + " " + minuteOfDayLayout)
	}
	return localTime.Format(minuteOfDayLayout)
}

func photoDayStayTimeRangeForHumanOutput(stay *photo.PhotoDayStay, localDate *presentation.CalendarDate) string {
	first := photoDayStayTimeForHumanOutput(stay.GetFirstPhotoCaptureLocalTime(), localDate)
	last := photoDayStayTimeForHumanOutput(stay.GetLastPhotoCaptureLocalTime(), localDate)
	if first == last {
		return first
	}
	return first + "–" + last
}

func writePhotoLocalDayListResponse(writer io.Writer, response *photo.PhotoLocalDayListResponse) error {
	records := response.GetPhotoLocalDayRecordsNewestFirst()
	if count := response.GetPhotosWithoutKnownCaptureLocalDayCount(); count > 0 {
		if err := writeLines(writer, Wrap(FormatInteger(int64(count))+" photos have no known capture zone and cannot be assigned to a local day.", OutputWidth(writer))); err != nil {
			return err
		}
	}
	if len(records) == 0 {
		return writeLine(writer, "No photo days match.")
	}
	if err := writeLines(writer, []string{fmt.Sprintf("Photo days: showing %s of %s.", FormatInteger(int64(len(records))), FormatInteger(int64(response.GetTotalMatchingLocalDayCount()))), ""}); err != nil {
		return err
	}
	columns := []TableColumn{archiveEventTimeColumn("day"), archiveEventTimeColumn("first photo"), archiveEventTimeColumn("last photo"), {Header: columnHeaderWhere, Wrap: true}}
	if response.GetKnownPlacesAreConfigured() {
		columns = append(columns, TableColumn{Header: "known / home", Wrap: true})
	}
	var rows [][]string
	var allStaysNextCommands []NextCommand
	for _, day := range records {
		first := day.GetFirstPhotoCaptureLocalTime()
		last := day.GetLastPhotoCaptureLocalTime()
		firstOffset, lastOffset := first.GetCaptureUtcOffsetSeconds(), last.GetCaptureUtcOffsetSeconds()
		startTime, _ := archiveEventTimeFromTimestamp(first.GetCaptureTimeUtc(), &firstOffset)
		endTime, _ := archiveEventTimeFromTimestamp(last.GetCaptureTimeUtc(), &lastOffset)
		var timedStays, knownPlaces []string
		previousHeading := ""
		stays := day.GetPhotoDayStaysInCaptureOrder()
		displayedStayCount := len(stays)
		if !response.GetShowAllPhotoDayStays() {
			displayedStayCount = min(displayedStayCount, maximumPhotoDayStaysForHumanOutput)
		}
		for _, stay := range stays[:displayedStayCount] {
			heading := photoDayStayHeadingForHumanOutput(stay)
			if heading != previousHeading {
				timedStays = append(timedStays, heading)
				previousHeading = heading
			}
			placeName := stay.GetMostSharedApplePlaceName()
			if uncertainty := strings.TrimSpace(photoLocationUncertaintyForHumanOutput(stay.GetCameraLocation())); uncertainty != "" {
				placeName = uncertainty + " · " + placeName
			}
			timeRange := photoDayStayTimeRangeForHumanOutput(stay, day.GetPhotoLocalDate())
			timedStays = append(timedStays, timeRange+"  "+placeName)
			knownPlace := stay.GetMatchedKnownPlaceName()
			if knownPlace == "" && stay.DistanceFromHomeMetres != nil {
				knownPlace = photoDistanceMetresForHumanOutput(stay.GetDistanceFromHomeMetres()) + " from home"
			}
			if knownPlace != "" {
				knownPlaces = append(knownPlaces, timeRange+" "+photoDayStayUTCOffsetForHumanOutput(stay.GetFirstPhotoCaptureLocalTime())+"  "+knownPlace)
			}
		}
		if additionalStays := len(stays) - displayedStayCount; additionalStays > 0 {
			timedStays = append(timedStays, "and "+FormatInteger(int64(additionalStays))+" more stays")
			date := formatCalendarDate(day.GetPhotoLocalDate())
			allStaysNextCommands = append(allStaysNextCommands, nextCommandWithLabel(nextCommandLabelAllStays, "photos", "days", "--from", date, "--to", date, "--all-stays"))
		}
		if withoutPlace := day.GetPhotosWithoutPlaceCount(); withoutPlace > 0 {
			photoNoun := "photos"
			if withoutPlace == 1 {
				photoNoun = "photo"
			}
			timedStays = append(timedStays, "("+FormatInteger(int64(withoutPlace))+" "+photoNoun+" without a place)")
		}
		row := []string{formatCalendarDate(day.GetPhotoLocalDate()), formatArchiveEventTimeWithinCalendarDate(startTime, day.GetPhotoLocalDate()), formatArchiveEventTimeWithinCalendarDate(endTime, day.GetPhotoLocalDate()), strings.Join(timedStays, "\n")}
		if response.GetKnownPlacesAreConfigured() {
			row = append(row, strings.Join(knownPlaces, "\n"))
		}
		rows = append(rows, row)
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(writer, columns, rows, 3); err != nil {
		return err
	}
	return writeNextCommands(writer, allStaysNextCommands)
}
