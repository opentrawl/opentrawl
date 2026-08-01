package calendar

import (
	"strings"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	calendarrecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar"
	"google.golang.org/protobuf/types/known/timestamppb"
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
		CalendarOwnerOrPurposeDescriptionStatedTime: timestamppb.New(
			annotation.CalendarOwnerOrPurposeDescriptionStatedTime,
		),
	}
}
