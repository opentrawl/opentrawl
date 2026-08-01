package calendar

import (
	"strings"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	calendarrecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func calendarOwnerOrPurposeAnnotationForProduct(
	annotation *archive.CalendarOwnerOrPurposeAnnotation,
) *calendarrecord.CalendarOwnerOrPurposeAnnotation {
	if annotation == nil {
		return nil
	}
	return &calendarrecord.CalendarOwnerOrPurposeAnnotation{
		CalendarOwnerOrPurposeDescription: strings.TrimSpace(
			annotation.CalendarOwnerOrPurposeDescription,
		),
		CalendarOwnerOrPurposeDescriptionStatedDate: &presentation.CalendarDate{
			CalendarYear: annotation.CalendarOwnerOrPurposeDescriptionStatedDate.CalendarYear,
			CalendarMonthNumber: annotation.
				CalendarOwnerOrPurposeDescriptionStatedDate.CalendarMonthNumber,
			CalendarDayOfMonth: annotation.
				CalendarOwnerOrPurposeDescriptionStatedDate.CalendarDayOfMonth,
		},
	}
}
