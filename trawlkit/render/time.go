package render

import (
	"regexp"
	"time"

	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Every time OpenTrawl shows is written here, on one line, in one of two
// forms. A list row, read to scan many records, shows the event's local time
// with its UTC offset; the offset already makes the instant exact. An opened
// record and help, read for detail, add the same instant in UTC.
//
//	list row                                 opened record and help
//	2026-10-11 11:05 +02:00                  2026-10-11 11:05 +02:00 (09:05 UTC)
//	2026-10-11 01:05 +02:00                  2026-10-11 01:05 +02:00 (2026-10-10 23:05 UTC)
//	2026-11-12 14:45–17:45 +01:00            2026-11-12 14:45–17:45 +01:00 (13:45–16:45 UTC)
//	2026-10-12 16:00 +02:00–20:25 +03:00     2026-10-12 16:00 +02:00–20:25 +03:00 (14:00–17:25 UTC)
//	2026-10-11 09:05 UTC                     the same: the local time is UTC itself
//	2026-10-12 (all day)                     the same: a calendar date has no time of day
//
// The local time is in the offset the source recorded for the event, such as
// a calendar event's own zone. Messages, conversations and notes record none,
// so they use this computer's time zone at that moment; the offset is always
// written, so the zone is never hidden. A date is written only where it
// differs from the date just before it. Both forms paste back into --after
// and --before.
const (
	calendarDateLayout             = "2006-01-02"
	minuteOfDayLayout              = "15:04"
	utcOffsetLayout                = "-07:00"
	coordinatedUniversalTimeSuffix = " UTC"
	timeRangeSeparator             = "–"
	allDayCalendarEventSuffix      = " (all day)"
	utcOffsetSecondsOfUTCItself    = 0
)

// writtenArchiveEventTimePattern matches every time written above, so text
// wrapping never breaks one across lines.
var writtenArchiveEventTimePattern = func() *regexp.Regexp {
	const (
		date      = `\d{4}-\d{2}-\d{2}`
		minute    = `\d{2}:\d{2}`
		utcOffset = `[+-]\d{2}:\d{2}`
	)
	localTime := date + ` ` + minute + `(?: ` + utcOffset + `)?` +
		`(?:–(?:` + date + ` )?` + minute + `(?: ` + utcOffset + `)?)?`
	utcTime := `(?: UTC| \((?:` + date + ` )?` + minute + `(?:–(?:` + date + ` )?` + minute + `)? UTC\))?`
	return regexp.MustCompile(localTime + utcTime)
}()

// archiveEventTimeWrittenForm says whether a time is written for a list row
// or for an opened record.
type archiveEventTimeWrittenForm int

const (
	localTimeWithUTCOffsetForListRow archiveEventTimeWrittenForm = iota
	localTimeWithUTCOffsetAndUTCForOpenedRecord
)

// archiveEventTime is an instant and the UTC offset it is shown in: the
// offset the source recorded, or this computer's.
type archiveEventTime struct {
	eventInstant          time.Time
	eventUTCOffsetSeconds int
}

func archiveEventTimeFromTimestamp(
	timestamp *timestamppb.Timestamp,
	eventUTCOffsetSecondsRecordedBySourceArchive *int32,
) (archiveEventTime, bool) {
	if timestamp == nil || !timestamp.IsValid() {
		return archiveEventTime{}, false
	}
	instant := timestamp.AsTime()
	if eventUTCOffsetSecondsRecordedBySourceArchive != nil {
		return archiveEventTime{
			eventInstant:          instant,
			eventUTCOffsetSeconds: int(*eventUTCOffsetSecondsRecordedBySourceArchive),
		}, true
	}
	_, thisComputerUTCOffsetSeconds := instant.In(time.Local).Zone()
	return archiveEventTime{eventInstant: instant, eventUTCOffsetSeconds: thisComputerUTCOffsetSeconds}, true
}

func (eventTime archiveEventTime) localTime() time.Time {
	return eventTime.eventInstant.In(time.FixedZone("", eventTime.eventUTCOffsetSeconds))
}

func (eventTime archiveEventTime) utcTime() time.Time {
	return eventTime.eventInstant.UTC()
}

func (eventTime archiveEventTime) isInUTCItself() bool {
	return eventTime.eventUTCOffsetSeconds == utcOffsetSecondsOfUTCItself
}

func formatArchiveEventTime(eventTime archiveEventTime, writtenForm archiveEventTimeWrittenForm) string {
	localTime := eventTime.localTime()
	if eventTime.isInUTCItself() {
		return localTime.Format(calendarDateLayout+" "+minuteOfDayLayout) + coordinatedUniversalTimeSuffix
	}
	localTimeWithUTCOffset := localTime.Format(calendarDateLayout + " " + minuteOfDayLayout + " " + utcOffsetLayout)
	if writtenForm == localTimeWithUTCOffsetForListRow {
		return localTimeWithUTCOffset
	}
	return localTimeWithUTCOffset +
		" (" + minuteWithDateWhenDifferent(eventTime.utcTime(), localTime) + coordinatedUniversalTimeSuffix + ")"
}

// formatArchiveEventTimeRange writes when something starts and ends. The
// start's offset is written only when the end is in another offset, as a
// flight that lands in another zone is.
func formatArchiveEventTimeRange(
	startTime archiveEventTime,
	endTime archiveEventTime,
	writtenForm archiveEventTimeWrittenForm,
) string {
	startLocalTime := startTime.localTime()
	endLocalTime := endTime.localTime()
	localRange := startLocalTime.Format(calendarDateLayout + " " + minuteOfDayLayout)
	if startTime.eventUTCOffsetSeconds != endTime.eventUTCOffsetSeconds {
		localRange += " " + startLocalTime.Format(utcOffsetLayout)
	}
	localRange += timeRangeSeparator + minuteWithDateWhenDifferent(endLocalTime, startLocalTime)
	if startTime.isInUTCItself() && endTime.isInUTCItself() {
		return localRange + coordinatedUniversalTimeSuffix
	}
	localRange += " " + endLocalTime.Format(utcOffsetLayout)
	if writtenForm == localTimeWithUTCOffsetForListRow {
		return localRange
	}
	utcStart := startTime.utcTime()
	utcRange := minuteWithDateWhenDifferent(utcStart, startLocalTime) + timeRangeSeparator +
		minuteWithDateWhenDifferent(endTime.utcTime(), utcStart)
	return localRange + " (" + utcRange + coordinatedUniversalTimeSuffix + ")"
}

// minuteWithDateWhenDifferent writes 15:04, or 2006-01-02 15:04 when the
// date is not the date the reader has just read.
func minuteWithDateWhenDifferent(timeToWrite time.Time, timeWrittenBefore time.Time) string {
	if sameCalendarDate(timeToWrite, timeWrittenBefore) {
		return timeToWrite.Format(minuteOfDayLayout)
	}
	return timeToWrite.Format(calendarDateLayout + " " + minuteOfDayLayout)
}

func sameCalendarDate(first time.Time, second time.Time) bool {
	firstYear, firstMonth, firstDay := first.Date()
	secondYear, secondMonth, secondDay := second.Date()
	return firstYear == secondYear && firstMonth == secondMonth && firstDay == secondDay
}

func formatCalendarDate(calendarDate *presentation.CalendarDate) string {
	if calendarDate == nil {
		return ""
	}
	year := calendarDate.GetCalendarYear()
	month := calendarDate.GetCalendarMonthNumber()
	day := calendarDate.GetCalendarDayOfMonth()
	if year < 1 || month < 1 || month > 12 || day < 1 || day > 31 {
		return ""
	}
	return time.Date(int(year), time.Month(month), int(day), 0, 0, 0, 0, time.UTC).Format(calendarDateLayout)
}

// formatArchiveRecordAssociatedTime writes the time a record is about: an
// exact time or a calendar date.
func formatArchiveRecordAssociatedTime(
	associatedTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
	writtenForm archiveEventTimeWrittenForm,
) string {
	if associatedTime == nil {
		return ""
	}
	switch typedTime := associatedTime.GetArchiveRecordAssociatedTime().(type) {
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime:
		eventTime, valid := archiveEventTimeFromTimestamp(
			typedTime.ExactTime,
			associatedTime.EventUtcOffsetSecondsRecordedBySourceArchive,
		)
		if !valid {
			return ""
		}
		return formatArchiveEventTime(eventTime, writtenForm)
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate:
		return formatCalendarDate(typedTime.CalendarDate)
	default:
		return ""
	}
}

// formatArchiveRecordAssociatedTimeRange writes when something such as a
// calendar event starts and ends.
func formatArchiveRecordAssociatedTimeRange(
	startTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
	endTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
	writtenForm archiveEventTimeWrittenForm,
) string {
	switch typedStartTime := startTime.GetArchiveRecordAssociatedTime().(type) {
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime:
		startEventTime, valid := archiveEventTimeFromTimestamp(
			typedStartTime.ExactTime,
			startTime.EventUtcOffsetSecondsRecordedBySourceArchive,
		)
		if !valid {
			return ""
		}
		typedEndTime, endIsExactTime := endTime.GetArchiveRecordAssociatedTime().(*presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime)
		if !endIsExactTime {
			return formatArchiveEventTime(startEventTime, writtenForm)
		}
		endEventTime, valid := archiveEventTimeFromTimestamp(
			typedEndTime.ExactTime,
			endTime.EventUtcOffsetSecondsRecordedBySourceArchive,
		)
		if !valid || endEventTime.eventInstant.Before(startEventTime.eventInstant) {
			return formatArchiveEventTime(startEventTime, writtenForm)
		}
		return formatArchiveEventTimeRange(startEventTime, endEventTime, writtenForm)
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate:
		startDate := formatCalendarDate(typedStartTime.CalendarDate)
		if startDate == "" {
			return ""
		}
		endDate := ""
		if typedEndTime, endIsCalendarDate := endTime.GetArchiveRecordAssociatedTime().(*presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate); endIsCalendarDate {
			endDate = formatCalendarDate(typedEndTime.CalendarDate)
		}
		if endDate == "" || endDate == startDate {
			return startDate + allDayCalendarEventSuffix
		}
		return startDate + timeRangeSeparator + endDate + allDayCalendarEventSuffix
	default:
		return ""
	}
}

// formatExactTimeInThisComputerTimeZone is for exact times whose source did
// not record where the event happened: messages, conversations, notes and
// archive updates.
func formatExactTimeInThisComputerTimeZone(
	timestamp *timestamppb.Timestamp,
	writtenForm archiveEventTimeWrittenForm,
) string {
	eventTime, valid := archiveEventTimeFromTimestamp(timestamp, nil)
	if !valid {
		return ""
	}
	return formatArchiveEventTime(eventTime, writtenForm)
}

// archiveEventTimeColumn is the one way a table shows a time: always whole,
// on one line.
func archiveEventTimeColumn(header string) TableColumn {
	return TableColumn{Header: header, NeverTruncateCellValues: true}
}
