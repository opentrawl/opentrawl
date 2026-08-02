package render

import (
	"io"
	"strings"

	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

type messageListDisplayRow struct {
	selected                  bool
	when                      string
	senderDisplayContext      string
	recipientDisplayContext   string
	conversationDisplayName   string
	displayedMessageOrMedia   string
	globallyRoutableTrawlLink string
}

func WriteTrawlerMessageListResponse(
	writer io.Writer,
	response *message.MessageListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return nil
	}
	conversationDisplayNameForRestrictedMessageRecords := strings.TrimSpace(
		response.GetConversationDisplayNameForMessageRecordsRestrictedToOneConversation(),
	)
	if conversationDisplayNameForRestrictedMessageRecords != "" {
		for _, headingLine := range Wrap(
			"Messages in "+conversationDisplayNameForRestrictedMessageRecords,
			OutputWidth(writer),
		) {
			if _, err := io.WriteString(writer, headingLine+"\n"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(writer, "\n"); err != nil {
			return err
		}
	}
	messageRecords := response.GetMessageRecordsNewestFirst()
	if len(messageRecords) == 0 {
		_, err := io.WriteString(writer, "No messages match.\n")
		return err
	}
	rows := make([]messageListDisplayRow, 0, len(messageRecords))
	for _, item := range messageRecords {
		if item == nil {
			continue
		}
		from := displayedPeopleWithRoles(
			item.GetPeopleRelatedToMessage(),
			person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
			person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_AUTHOR,
		)
		recipients := displayedPeopleWithRoles(
			item.GetPeopleRelatedToMessage(),
			person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
		)
		conversationDisplayName := strings.TrimSpace(item.GetConversationDisplayName())
		if conversationDisplayNameForRestrictedMessageRecords != "" {
			conversationDisplayName = ""
		}
		rows = append(rows, messageListDisplayRow{
			when:                    trawlerSpecificCommandAssociatedTime(item.GetMessageTime()),
			senderDisplayContext:    from,
			recipientDisplayContext: strings.TrimSpace(recipients),
			conversationDisplayName: conversationDisplayName,
			displayedMessageOrMedia: messageTextAndMediaForHumanOutput(
				item.GetMessageText(),
				item.GetMessageMedia(),
			),
			globallyRoutableTrawlLink: globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						item.GetCanonicalRecordReference(),
					),
			),
		})
	}
	return writeMessageListRows(writer, rows)
}
