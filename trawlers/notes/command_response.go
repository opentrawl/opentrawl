package notes

import (
	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func notesListCommandResponse(
	columnDisplayNamesInOrder []string,
	rowsInDisplayOrder []*presentation.TrawlerSpecificCommandListPresentationRow,
	totalRowCount uint64,
	moreRowsExist bool,
) *command.TrawlerCommandResponse {
	return notesTrawlerSpecificCommandResponse(&command.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
			TrawlerSpecificCommandListPresentation: &presentation.TrawlerSpecificCommandListPresentation{
				ColumnDisplayNamesInOrder: append([]string(nil), columnDisplayNamesInOrder...),
				RowsInDisplayOrder:        rowsInDisplayOrder,
				TotalRowCount: &presentation.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
					ExactTotalRowCount: totalRowCount,
				},
				MoreRowsExist: moreRowsExist,
			},
		},
	})
}

func notesDetailCommandResponse(
	detailDisplayName string,
	fieldsInDisplayOrder []*presentation.TrawlerSpecificCommandDetailPresentationField,
) *command.TrawlerCommandResponse {
	return notesTrawlerSpecificCommandResponse(&command.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
			TrawlerSpecificCommandDetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
				DetailDisplayName:    detailDisplayName,
				FieldsInDisplayOrder: fieldsInDisplayOrder,
			},
		},
	})
}

func notesTrawlerSpecificCommandResponse(
	trawlerSpecificCommandResponse *command.TrawlerSpecificCommandResponse,
) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: trawlerSpecificCommandResponse,
		},
	}
}

func notesListRow(
	columnValuesInDisplayOrder ...*presentation.TrawlerSpecificCommandPresentationValue,
) *presentation.TrawlerSpecificCommandListPresentationRow {
	return &presentation.TrawlerSpecificCommandListPresentationRow{
		ColumnValuesInDisplayOrder: columnValuesInDisplayOrder,
	}
}

func notesPresentationTextValue(textValue string) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
	}
}

func notesPresentationUnsignedCountValue(count int64) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount{
			UnsignedCount: uint64(max(count, 0)),
		},
	}
}

func notesPresentationTimeValue(storedTime string) *presentation.TrawlerSpecificCommandPresentationValue {
	parsedTime := parseNotesArchiveTimeForPresentation(storedTime)
	if parsedTime.IsZero() {
		return &presentation.TrawlerSpecificCommandPresentationValue{}
	}
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTimeForDisplay: &presentation.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
					ExactTime: timestamppb.New(parsedTime),
				},
			},
		},
	}
}

func notesPresentationCanonicalRecordReferenceValue(
	canonicalRecordReference string,
) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(canonicalRecordReference),
		},
	}
}

func notesDetailTextField(
	fieldDisplayName string,
	textValue string,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationTextValue(textValue),
	}
}

func notesDetailUnsignedCountField(
	fieldDisplayName string,
	count int64,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationUnsignedCountValue(count),
	}
}

func notesDetailTimeField(
	fieldDisplayName string,
	storedTime string,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationTimeValue(storedTime),
	}
}

func notesDetailCanonicalRecordReferenceField(
	fieldDisplayName string,
	canonicalRecordReference string,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       notesPresentationCanonicalRecordReferenceValue(canonicalRecordReference),
	}
}
