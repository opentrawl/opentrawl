package render

import (
	"io"
	"strings"

	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

func writeTrawlerMessageListResponse(
	writer io.Writer,
	response *message.MessageListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return nil
	}
	conversationDisplayNameForMessagesInOneConversation := strings.TrimSpace(
		response.GetConversationDisplayNameForMessageRecordsRestrictedToOneConversation(),
	)
	if conversationDisplayNameForMessagesInOneConversation != "" {
		if err := writeLines(writer, Wrap(
			"Messages in "+conversationDisplayNameForMessagesInOneConversation,
			OutputWidth(writer),
		)); err != nil {
			return err
		}
		if err := writeLine(writer, ""); err != nil {
			return err
		}
	}
	messageRecords := response.GetMessageRecordsNewestFirst()
	if len(messageRecords) == 0 {
		return writeLine(writer, "No messages match.")
	}
	rows := make([]messageListDisplayRow, 0, len(messageRecords))
	for _, messageRecord := range messageRecords {
		if messageRecord == nil {
			continue
		}
		row := messageListDisplayRow{
			when: formatArchiveRecordAssociatedTime(messageRecord.GetMessageTime(), localTimeWithUTCOffsetForListRow),
			displayedMessageOrMedia: messageTextAndMediaForHumanOutput(
				messageRecord.GetMessageText(),
				messageRecord.GetMessageMedia(),
			),
			globallyRoutableTrawlLink: globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						messageRecord.GetCanonicalRecordReference(),
					),
			),
		}
		row.who = formatSendersAndRecipients(messageRecord.GetPeopleRelatedToMessage())
		if conversationDisplayNameForMessagesInOneConversation == "" {
			row.where = strings.Join(containerNamesThatAreNotPeople(
				[]string{messageRecord.GetConversationDisplayName()},
				messageRecord.GetPeopleRelatedToMessage(),
			), "")
		}
		rows = append(rows, row)
	}
	return writeMessageListRows(writer, rows)
}
