package render

import (
	"strings"

	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func presentationValueFromTrawlerSpecificCommand(
	value *presentation.TrawlerSpecificCommandPresentationValue,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	timeWrittenForm archiveEventTimeWrittenForm,
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
		return formatArchiveRecordAssociatedTime(typedValue.ArchiveRecordAssociatedTimeForDisplay, timeWrittenForm)
	case *presentation.TrawlerSpecificCommandPresentationValue_ExactMonetaryAmount:
		return FormatExactMonetaryAmount(typedValue.ExactMonetaryAmount)
	default:
		return ""
	}
}

func presentationValueIsExactMonetaryAmount(value *presentation.TrawlerSpecificCommandPresentationValue) bool {
	_, isMonetaryAmount := value.GetTypedValue().(*presentation.TrawlerSpecificCommandPresentationValue_ExactMonetaryAmount)
	return isMonetaryAmount
}

func presentationValueIsArchiveRecordAssociatedTime(value *presentation.TrawlerSpecificCommandPresentationValue) bool {
	_, isArchiveRecordAssociatedTime :=
		value.GetTypedValue().(*presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay)
	return isArchiveRecordAssociatedTime
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
