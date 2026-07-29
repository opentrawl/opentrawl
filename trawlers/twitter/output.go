package twitter

import (
	"strings"
	"time"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func twitterMessageListCommandResponse(value listEnvelope) *commandv1.TrawlerCommandResponse {
	messageRecords := make([]*messagev1.MessageRecord, 0, len(value.Results))
	for _, item := range value.Results {
		var people []*personv1.PersonRelatedToArchiveRecord
		if personDisplayName := strings.TrimSpace(item.Who); personDisplayName != "" {
			people = []*personv1.PersonRelatedToArchiveRecord{{
				PersonDisplayName:         personDisplayName,
				PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_AUTHOR,
			}}
		}
		messageRecords = append(messageRecords, &messagev1.MessageRecord{
			MessageTime: twitterArchiveRecordAssociatedTime(item.timeValue),
			CanonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment: item.Ref,
			PeopleRelatedToMessage:      people,
			DisplayedMessageOrMediaText: item.Text,
		})
	}
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_MessageListResponse{
			MessageListResponse: &messagev1.MessageListResponse{
				MessageRecordsInDisplayOrder: messageRecords,
				TotalMatchingMessageCount:    uint64(max(value.Total, 0)),
				MoreMatchingMessagesExist:    value.Truncated,
			},
		},
	}
}

func twitterStatsCommandResponse(value statsEnvelope) *commandv1.TrawlerCommandResponse {
	rows := make([]*presentationv1.TrawlerSpecificCommandListPresentationRow, 0, len(value.Results))
	for _, result := range value.Results {
		rows = append(rows, &presentationv1.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentationv1.TrawlerSpecificCommandPresentationValue{
				twitterPresentationExactTimeValue(result.timeValue),
				twitterPresentationUnsignedCountValue(result.Count),
				twitterPresentationCanonicalRecordReferenceValue(result.Ref),
				twitterPresentationTextValue(result.Text),
			},
		})
	}
	return twitterTrawlerSpecificCommandResponse(&commandv1.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
			TrawlerSpecificCommandListPresentation: &presentationv1.TrawlerSpecificCommandListPresentation{
				ColumnDisplayNamesInOrder: []string{"Date", humanLabel(value.By), "Link", "Text"},
				RowsInDisplayOrder:        rows,
				TotalRowCount: &presentationv1.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
					ExactTotalRowCount: uint64(max(value.Population, 0)),
				},
				MoreRowsExist: value.Population > len(rows),
			},
		},
	})
}

func twitterSpendCommandResponse(value spendEnvelope) *commandv1.TrawlerCommandResponse {
	return twitterDetailCommandResponse("Monthly X API spend",
		twitterDetailTextField("Month", value.Month),
		twitterDetailTextField("Spent", "$"+value.SpentUSD),
		twitterDetailTextField("Cap", "$"+value.MonthlyBudgetUSD),
		twitterDetailTextField("Remaining", "$"+value.RemainingUSD))
}

func twitterImportCommandResponse(value importEnvelope) *commandv1.TrawlerCommandResponse {
	return twitterDetailCommandResponse("Archive imported",
		twitterDetailUnsignedCountField("Tweets", int64(value.Tweets)),
		twitterDetailUnsignedCountField("Authored", int64(value.Authored)),
		twitterDetailUnsignedCountField("Likes seen", int64(value.LikesSeen)),
		twitterDetailUnsignedCountField("Profiles", int64(value.Profiles)),
		twitterDetailUnsignedCountField("Long-form notes merged", int64(value.NoteTweetsMerged)),
		twitterDetailUnsignedCountField("Long-form notes unmatched", int64(value.NoteTweetsUnmatched)),
		twitterDetailUnsignedCountField("Likes without text", int64(value.LikesWithoutText)))
}

func twitterDetailCommandResponse(
	detailDisplayName string,
	fields ...*presentationv1.TrawlerSpecificCommandDetailPresentationField,
) *commandv1.TrawlerCommandResponse {
	return twitterTrawlerSpecificCommandResponse(&commandv1.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
			TrawlerSpecificCommandDetailPresentation: &presentationv1.TrawlerSpecificCommandDetailPresentation{
				DetailDisplayName:    detailDisplayName,
				FieldsInDisplayOrder: fields,
			},
		},
	})
}

func twitterTrawlerSpecificCommandResponse(
	trawlerSpecificCommandResponse *commandv1.TrawlerSpecificCommandResponse,
) *commandv1.TrawlerCommandResponse {
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: trawlerSpecificCommandResponse,
		},
	}
}

func twitterPresentationTextValue(textValue string) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
	}
}

func twitterPresentationUnsignedCountValue(count int64) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_UnsignedCount{
			UnsignedCount: uint64(max(count, 0)),
		},
	}
}

func twitterPresentationCanonicalRecordReferenceValue(
	canonicalRecordReference string,
) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment{
			CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment: canonicalRecordReference,
		},
	}
}

func twitterPresentationExactTimeValue(exactTime time.Time) *presentationv1.TrawlerSpecificCommandPresentationValue {
	if exactTime.IsZero() {
		return &presentationv1.TrawlerSpecificCommandPresentationValue{}
	}
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTimeForDisplay: twitterArchiveRecordAssociatedTime(exactTime),
		},
	}
}

func twitterDetailTextField(
	fieldDisplayName string,
	textValue string,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       twitterPresentationTextValue(textValue),
	}
}

func twitterDetailUnsignedCountField(
	fieldDisplayName string,
	count int64,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       twitterPresentationUnsignedCountValue(count),
	}
}

func twitterArchiveRecordAssociatedTime(exactTime time.Time) *presentationv1.ArchiveRecordAssociatedTimeForDisplay {
	if exactTime.IsZero() {
		return nil
	}
	return &presentationv1.ArchiveRecordAssociatedTimeForDisplay{ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(exactTime)}}
}

func humanName(value, authorID, ownerAuthorID string) string {
	if ownerAuthorID != "" && authorID == ownerAuthorID {
		return selfDisplayName(value)
	}
	return value
}

func selfDisplayName(value string) string {
	if handle := displayHandle(value); handle != "" {
		return "me (" + handle + ")"
	}
	return "me"
}

func postAuthorDisplayName(value, authorID, ownerAuthorID string) string {
	return humanName(value, authorID, ownerAuthorID)
}

func displayHandle(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@") {
		return strings.Fields(value)[0]
	}
	start := strings.LastIndex(value, "(@")
	if start < 0 || !strings.HasSuffix(value, ")") {
		return ""
	}
	return strings.TrimSuffix(value[start+1:], ")")
}

func humanLabel(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
