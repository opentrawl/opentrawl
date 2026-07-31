package photos

import (
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func photosDetailCommandResponse(
	detailDisplayName string,
	fields ...*presentation.TrawlerSpecificCommandDetailPresentationField,
) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
					TrawlerSpecificCommandDetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
						DetailDisplayName:    detailDisplayName,
						FieldsInDisplayOrder: fields,
					},
				},
			},
		},
	}
}

func photosDetailTextField(
	fieldDisplayName string,
	textValue string,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
		},
	}
}

func photosDetailUnsignedCountField(
	fieldDisplayName string,
	count int64,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount{
				UnsignedCount: uint64(max(count, 0)),
			},
		},
	}
}

func photosDetailExactTimeField(
	fieldDisplayName string,
	exactTime time.Time,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentation.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
						ExactTime: timestamppb.New(exactTime),
					},
				},
			},
		},
	}
}

func photosDetailCanonicalRecordReferenceField(
	fieldDisplayName string,
	canonicalRecordReference string,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference{
				CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(canonicalRecordReference),
			},
		},
	}
}
