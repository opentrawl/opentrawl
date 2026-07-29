package photos

import (
	"time"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func photosDetailCommandResponse(
	detailDisplayName string,
	fields ...*presentationv1.TrawlerSpecificCommandDetailPresentationField,
) *commandv1.TrawlerCommandResponse {
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &commandv1.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
					TrawlerSpecificCommandDetailPresentation: &presentationv1.TrawlerSpecificCommandDetailPresentation{
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
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
		},
	}
}

func photosDetailUnsignedCountField(
	fieldDisplayName string,
	count int64,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_UnsignedCount{
				UnsignedCount: uint64(max(count, 0)),
			},
		},
	}
}

func photosDetailExactTimeField(
	fieldDisplayName string,
	exactTime time.Time,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
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
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment{
				CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment: canonicalRecordReference,
			},
		},
	}
}
