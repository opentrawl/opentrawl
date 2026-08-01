package render

import (
	"fmt"
	"io"
	"strings"

	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

const (
	messageListWideOutputMinimumWidth           = 99
	messageListWhenColumnWidth                  = 16
	messageListMinimumUsefulWideTextColumnWidth = 40
	messageListMaximumSenderColumnWidth         = 16
	messageListMaximumContextWidth              = 22
)

type messageListDisplayRow struct {
	when                       string
	senderDisplayContext       string
	recipientDisplayContext    string
	conversationDisplayContext string
	displayedMessageOrMedia    string
	globallyRoutableTrawlLink  string
}

func WriteTrawlerMessageListResponse(
	writer io.Writer,
	response *message.MessageListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return nil
	}
	scopedConversationDisplayContext := strings.TrimSpace(
		response.GetConversationDisplayContextWhenMessagesAreRestrictedToOneConversation(),
	)
	if scopedConversationDisplayContext != "" {
		if err := WriteWrappedField(writer, "Conversation", scopedConversationDisplayContext); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
		}
	}
	messageRecords := response.GetMessageRecordsInDisplayOrder()
	if len(messageRecords) == 0 {
		_, err := fmt.Fprintln(writer, "No messages match.")
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
		conversation := strings.TrimSpace(item.GetConversationDisplayContext())
		if scopedConversationDisplayContext != "" {
			recipients = ""
			conversation = ""
		} else {
			recipients, conversation = messageListContextNeededAcrossConversations(from, recipients, conversation)
		}
		rows = append(rows, messageListDisplayRow{
			when:                       trawlerSpecificCommandAssociatedTime(item.GetMessageTime()),
			senderDisplayContext:       from,
			recipientDisplayContext:    recipients,
			conversationDisplayContext: conversation,
			displayedMessageOrMedia:    strings.TrimSpace(item.GetDisplayedMessageOrMediaText()),
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

func messageListContextNeededAcrossConversations(from, recipients, conversation string) (string, string) {
	from = strings.TrimSpace(from)
	recipients = strings.TrimSpace(recipients)
	conversation = strings.TrimSpace(conversation)
	if !strings.EqualFold(from, "me") && strings.EqualFold(recipients, "me") {
		recipients = ""
	}
	if strings.EqualFold(conversation, from) || strings.EqualFold(conversation, recipients) {
		conversation = ""
	}
	return recipients, conversation
}
