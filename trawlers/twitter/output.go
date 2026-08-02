package twitter

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func twitterMessageListCommandResponse(value listEnvelope) *command.TrawlerCommandResponse {
	messageRecords := make([]*message.MessageRecord, 0, len(value.Results))
	for _, item := range value.Results {
		var people []*person.PersonRelatedToArchiveRecord
		if personDisplayName := strings.TrimSpace(item.Who); personDisplayName != "" {
			people = []*person.PersonRelatedToArchiveRecord{{
				PersonDisplayName:         personDisplayName,
				PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_AUTHOR,
			}}
		}
		messageRecords = append(messageRecords, &message.MessageRecord{
			MessageTime:              twitterArchiveRecordAssociatedTime(item.timeValue),
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(item.Ref),
			PeopleRelatedToMessage:   people,
			MessageText:              item.Text,
		})
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_MessageListResponse{
			MessageListResponse: &message.MessageListResponse{
				MessageRecordsNewestFirst: messageRecords,
				TotalMatchingMessageCount: uint64(max(value.Total, 0)),
				MoreMatchingMessagesExist: value.Truncated,
			},
		},
	}
}

func twitterStatsCommandResponse(value statsEnvelope) *command.TrawlerCommandResponse {
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(value.Results))
	for _, result := range value.Results {
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				twitterPresentationExactTimeValue(result.timeValue),
				twitterPresentationUnsignedCountValue(result.Count),
				twitterPresentationCanonicalRecordReferenceValue(result.Ref),
				twitterPresentationTextValue(result.Text),
			},
		})
	}
	return twitterTrawlerSpecificCommandResponse(&command.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
			TrawlerSpecificCommandListPresentation: &presentation.TrawlerSpecificCommandListPresentation{
				ColumnDisplayNamesInOrder: []string{"Date", humanLabel(value.By), "Link", "Text"},
				RowsInDisplayOrder:        rows,
				TotalRowCount: &presentation.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
					ExactTotalRowCount: uint64(max(value.Population, 0)),
				},
				MoreRowsExist: value.Population > len(rows),
			},
		},
	})
}

func twitterSpendCommandResponse(value spendEnvelope) *command.TrawlerCommandResponse {
	return twitterDetailCommandResponse("Monthly X API spend",
		twitterDetailTextField("Month", value.Month),
		twitterDetailTextField("Spent", "$"+value.SpentUSD),
		twitterDetailTextField("Cap", "$"+value.MonthlyBudgetUSD),
		twitterDetailTextField("Remaining", "$"+value.RemainingUSD))
}

func twitterImportCommandResponse(value importEnvelope) *command.TrawlerCommandResponse {
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
	fields ...*presentation.TrawlerSpecificCommandDetailPresentationField,
) *command.TrawlerCommandResponse {
	return twitterTrawlerSpecificCommandResponse(&command.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
			TrawlerSpecificCommandDetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
				DetailDisplayName:    detailDisplayName,
				FieldsInDisplayOrder: fields,
			},
		},
	})
}

func twitterTrawlerSpecificCommandResponse(
	trawlerSpecificCommandResponse *command.TrawlerSpecificCommandResponse,
) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: trawlerSpecificCommandResponse,
		},
	}
}

func twitterPresentationTextValue(textValue string) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
	}
}

func twitterPresentationUnsignedCountValue(count int64) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount{
			UnsignedCount: uint64(max(count, 0)),
		},
	}
}

func twitterPresentationCanonicalRecordReferenceValue(
	canonicalRecordReference string,
) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(canonicalRecordReference),
		},
	}
}

func twitterPresentationExactTimeValue(exactTime time.Time) *presentation.TrawlerSpecificCommandPresentationValue {
	if exactTime.IsZero() {
		return &presentation.TrawlerSpecificCommandPresentationValue{}
	}
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTimeForDisplay: twitterArchiveRecordAssociatedTime(exactTime),
		},
	}
}

func twitterDetailTextField(
	fieldDisplayName string,
	textValue string,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       twitterPresentationTextValue(textValue),
	}
}

func twitterDetailUnsignedCountField(
	fieldDisplayName string,
	count int64,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       twitterPresentationUnsignedCountValue(count),
	}
}

func twitterArchiveRecordAssociatedTime(exactTime time.Time) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	if exactTime.IsZero() {
		return nil
	}
	return &presentation.ArchiveRecordAssociatedTimeForDisplay{ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(exactTime)}}
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
