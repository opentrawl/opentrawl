package notes

import (
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func notesListCommandResponse(
	columnDisplayNamesInOrder []string,
	rowsInDisplayOrder []*presentationv1.TrawlerSpecificCommandListPresentationRow,
	totalRowCount uint64,
	moreRowsExist bool,
) *commandv1.TrawlerCommandResponse {
	return notesTrawlerSpecificCommandResponse(&commandv1.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
			TrawlerSpecificCommandListPresentation: &presentationv1.TrawlerSpecificCommandListPresentation{
				ColumnDisplayNamesInOrder: append([]string(nil), columnDisplayNamesInOrder...),
				RowsInDisplayOrder:        rowsInDisplayOrder,
				TotalRowCount: &presentationv1.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
					ExactTotalRowCount: totalRowCount,
				},
				MoreRowsExist: moreRowsExist,
			},
		},
	})
}

func notesDetailCommandResponse(
	detailDisplayName string,
	fieldsInDisplayOrder []*presentationv1.TrawlerSpecificCommandDetailPresentationField,
) *commandv1.TrawlerCommandResponse {
	return notesTrawlerSpecificCommandResponse(&commandv1.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
			TrawlerSpecificCommandDetailPresentation: &presentationv1.TrawlerSpecificCommandDetailPresentation{
				DetailDisplayName:    detailDisplayName,
				FieldsInDisplayOrder: fieldsInDisplayOrder,
			},
		},
	})
}

func notesTrawlerSpecificCommandResponse(
	trawlerSpecificCommandResponse *commandv1.TrawlerSpecificCommandResponse,
) *commandv1.TrawlerCommandResponse {
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: trawlerSpecificCommandResponse,
		},
	}
}

func notesListRow(
	columnValuesInDisplayOrder ...*presentationv1.TrawlerSpecificCommandPresentationValue,
) *presentationv1.TrawlerSpecificCommandListPresentationRow {
	return &presentationv1.TrawlerSpecificCommandListPresentationRow{
		ColumnValuesInDisplayOrder: columnValuesInDisplayOrder,
	}
}

func notesPresentationTextValue(textValue string) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
	}
}

func notesPresentationUnsignedCountValue(count int64) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_UnsignedCount{
			UnsignedCount: uint64(max(count, 0)),
		},
	}
}

func notesPresentationTimeValue(storedTime string) *presentationv1.TrawlerSpecificCommandPresentationValue {
	parsedTime := parseNotesArchiveTimeForPresentation(storedTime)
	if parsedTime.IsZero() {
		return &presentationv1.TrawlerSpecificCommandPresentationValue{}
	}
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTimeForDisplay: &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
					ExactTime: timestamppb.New(parsedTime),
				},
			},
		},
	}
}

func notesPresentationCanonicalRecordReferenceValue(
	canonicalRecordReference string,
) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment{
			CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment: canonicalRecordReference,
		},
	}
}

func notesDetailTextField(
	fieldDisplayName string,
	textValue string,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationTextValue(textValue),
	}
}

func notesDetailUnsignedCountField(
	fieldDisplayName string,
	count int64,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationUnsignedCountValue(count),
	}
}

func notesDetailTimeField(
	fieldDisplayName string,
	storedTime string,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationTimeValue(storedTime),
	}
}

func notesDetailCanonicalRecordReferenceField(
	fieldDisplayName string,
	canonicalRecordReference string,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationCanonicalRecordReferenceValue(canonicalRecordReference),
	}
}
