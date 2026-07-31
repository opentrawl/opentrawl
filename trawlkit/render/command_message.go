package render

import (
	"fmt"
	"io"
	"strings"

	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
)

func WriteTrawlerMessageListResponse(
	writer io.Writer,
	response *messagev1.MessageListResponse,
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
	showConversation := scopedConversationDisplayContext == ""
	if showConversation {
		showConversation = false
		for _, messageRecord := range messageRecords {
			if messageConversationDisplayContext(messageRecord) != "" {
				showConversation = true
				break
			}
		}
	}
	columns := []TableColumn{
		{Header: "when", MinimumWidth: 16},
		{Header: "link", NeverTruncateCellValues: true},
		{Header: "from", Wrap: true},
		{Header: "to", Wrap: true},
	}
	if showConversation {
		columns = append(columns, TableColumn{
			Header: "conversation", MinimumWidth: len("conversation"), Wrap: true,
		})
	}
	columns = append(columns, TableColumn{Header: "text", Wrap: true, MaximumWrappedLines: 2})
	rows := make([][]string, 0, len(messageRecords))
	for _, item := range messageRecords {
		if item == nil {
			continue
		}
		row := []string{
			trawlerSpecificCommandAssociatedTime(item.GetMessageTime()),
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						item.GetCanonicalRecordReference(),
					),
			),
			displayedPeopleWithRoles(
				item.GetPeopleRelatedToMessage(),
				personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
				personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_AUTHOR,
			),
			displayedPeopleWithRoles(
				item.GetPeopleRelatedToMessage(),
				personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
			),
		}
		if showConversation {
			row = append(row, messageConversationDisplayContext(item))
		}
		row = append(row, strings.TrimSpace(item.GetDisplayedMessageOrMediaText()))
		rows = append(rows, row)
	}
	return WriteTable(writer, columns, rows)
}

func messageConversationDisplayContext(item *messagev1.MessageRecord) string {
	conversation := strings.TrimSpace(item.GetConversationDisplayContext())
	from := displayedPeopleWithRoles(
		item.GetPeopleRelatedToMessage(),
		personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_AUTHOR,
	)
	to := displayedPeopleWithRoles(
		item.GetPeopleRelatedToMessage(),
		personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	)
	if strings.EqualFold(conversation, strings.TrimSpace(from)) || strings.EqualFold(conversation, strings.TrimSpace(to)) {
		return ""
	}
	return conversation
}
