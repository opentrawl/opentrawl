package render

import (
	"strings"
	"time"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func trawlerSpecificCommandAssociatedTime(value *presentation.ArchiveRecordAssociatedTimeForDisplay) string {
	if value == nil {
		return ""
	}
	switch typedTime := value.GetArchiveRecordAssociatedTime().(type) {
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime:
		if typedTime.ExactTime == nil || !typedTime.ExactTime.IsValid() {
			return ""
		}
		return ShortLocalTime(typedTime.ExactTime.AsTime())
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate:
		return trawlerSpecificCommandCalendarDate(typedTime.CalendarDate)
	default:
		return ""
	}
}

func trawlerSpecificCommandCalendarDate(value *presentation.CalendarDate) string {
	if value == nil {
		return ""
	}
	year := value.GetCalendarYear()
	month := value.GetCalendarMonthNumber()
	day := value.GetCalendarDayOfMonth()
	if year < 1 || month < 1 || month > 12 || day < 1 || day > 31 {
		return ""
	}
	return time.Date(int(year), time.Month(month), int(day), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func presentationValueFromTrawlerSpecificCommand(
	value *presentation.TrawlerSpecificCommandPresentationValue,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) string {
	if value == nil {
		return ""
	}
	switch typedValue := value.GetTypedValue().(type) {
	case *presentation.TrawlerSpecificCommandPresentationValue_Text:
		return strings.TrimSpace(typedValue.Text)
	case *presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount:
		return FormatInteger(int64(typedValue.UnsignedCount))
	case *presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference:
		return globallyRoutableTrawlLinkText(
			globallyRoutableTrawlLinksByCanonicalRecordReference.
				globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
					typedValue.CanonicalRecordReference,
				),
		)
	case *presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay:
		return trawlerSpecificCommandAssociatedTime(typedValue.ArchiveRecordAssociatedTimeForDisplay)
	default:
		return ""
	}
}

func presentationValueIsCanonicalRecordReference(
	value *presentation.TrawlerSpecificCommandPresentationValue,
) bool {
	if value == nil {
		return false
	}
	_, isCanonicalRecordReference :=
		value.GetTypedValue().(*presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference)
	return isCanonicalRecordReference
}

func displayedPeopleWithRoles(
	people []*person.PersonRelatedToArchiveRecord,
	roles ...person.PersonRoleInArchiveRecord,
) string {
	includedRoles := make(map[person.PersonRoleInArchiveRecord]struct{}, len(roles))
	for _, role := range roles {
		includedRoles[role] = struct{}{}
	}
	values := make([]string, 0, len(people))
	for _, person := range people {
		if person != nil {
			if len(includedRoles) > 0 {
				if _, included := includedRoles[person.GetPersonRoleInArchiveRecord()]; !included {
					continue
				}
			}
			if name := strings.TrimSpace(person.GetPersonDisplayName()); name != "" {
				values = append(values, name)
			}
		}
	}
	return strings.Join(values, ", ")
}
