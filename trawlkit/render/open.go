package render

import (
	"fmt"
	"io"
	"strings"

	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	note "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

type OpenResponseRenderContext struct {
	TrawlerSpecificOpenedRecordPresentationActions TrawlerSpecificCommandActions
}

func WriteOpenResponse(
	writer io.Writer,
	response *open.OpenResponse,
	context OpenResponseRenderContext,
) error {
	if response == nil {
		return fmt.Errorf("open response is missing")
	}
	if response.GetFailure() != nil {
		_, err := fmt.Fprintln(writer, strings.TrimSpace(response.GetFailure().GetFailureMessage()))
		return err
	}
	record := response.GetRecord()
	if record == nil {
		return fmt.Errorf("open response has no record")
	}
	switch typedOpenedRecord := record.GetTypedOpenedRecord().(type) {
	case *open.OpenRecord_OpenedMessageRecordWithConversationContext:
		return WriteOpenedMessageRecordWithConversationContext(
			writer,
			typedOpenedRecord.OpenedMessageRecordWithConversationContext,
		)
	case *open.OpenRecord_ConversationRecord:
		return writeConversationRecord(
			writer,
			typedOpenedRecord.ConversationRecord,
			response.GetRequestedTrawlLink(),
		)
	case *open.OpenRecord_PersonRecord:
		return WritePersonRecord(
			writer,
			typedOpenedRecord.PersonRecord,
			response.GetRequestedTrawlLink(),
		)
	case *open.OpenRecord_CalendarEventRecord:
		return writeCalendarEventRecord(
			writer,
			typedOpenedRecord.CalendarEventRecord,
			response.GetRequestedTrawlLink(),
		)
	case *open.OpenRecord_OpenedNoteRecord:
		return writeOpenedNoteRecord(
			writer,
			typedOpenedRecord.OpenedNoteRecord,
			response.GetRequestedTrawlLink(),
		)
	case *open.OpenRecord_TrawlerSpecificOpenedRecordPresentation:
		trawlerSpecificOpenedRecordPresentation := typedOpenedRecord.TrawlerSpecificOpenedRecordPresentation
		if trawlerSpecificOpenedRecordPresentation == nil {
			return fmt.Errorf("trawler-specific opened record is missing")
		}
		globallyRoutableTrawlLinksByCanonicalRecordReference :=
			GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference{{
				CanonicalArchiveRecordReference: record.GetCanonicalRecordReference(),
				GloballyRoutableTrawlLink:       response.GetRequestedTrawlLink(),
			}}
		return WriteTrawlerSpecificCommandDetailPresentation(
			writer,
			trawlerSpecificOpenedRecordPresentation.GetDetailPresentation(),
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			context.TrawlerSpecificOpenedRecordPresentationActions.DetailActionsInDisplayOrder,
		)
	default:
		return fmt.Errorf("open record has no typed record")
	}
}

func writeOpenedNoteRecord(
	writer io.Writer,
	openedNoteRecord *note.OpenedNoteRecord,
	requestedTrawlLink *identity.GloballyRoutableTrawlLink,
) error {
	if openedNoteRecord == nil {
		return fmt.Errorf("opened note record is missing")
	}
	noteDisplayName := strings.TrimSpace(openedNoteRecord.GetNoteDisplayName())
	if noteDisplayName == "" {
		noteDisplayName = "Note"
	}
	fields := make([]CardField, 0, 5)
	if folderDisplayName := strings.TrimSpace(openedNoteRecord.GetNoteFolderDisplayName()); folderDisplayName != "" {
		fields = append(fields, CardField{Label: "Folder", Value: folderDisplayName})
	}
	if createdTime := exactTimestampForHumanOutput(openedNoteRecord.GetNoteCreatedTime()); createdTime != "" {
		fields = append(fields, CardField{Label: "Created", Value: createdTime})
	}
	if openedNoteRecord.GetSpecificRecoveredNoteVersionWasOpened() {
		if recoveredVersionTime := exactTimestampForHumanOutput(openedNoteRecord.GetOpenedNoteVersionTime()); recoveredVersionTime != "" {
			fields = append(fields, CardField{Label: "Recovered version", Value: recoveredVersionTime})
		}
	} else if modifiedTime := exactTimestampForHumanOutput(openedNoteRecord.GetNoteModifiedTime()); modifiedTime != "" {
		fields = append(fields, CardField{Label: "Modified", Value: modifiedTime})
	}
	fields = append(fields, CardField{
		Label: "Versions",
		Value: FormatInteger(int64(openedNoteRecord.GetRecoveredNoteVersionCount())),
	})
	if openedNoteRecord.GetRecoveredNoteVersionCount() > 0 {
		fields = append(fields, CardField{
			Label: "List versions",
			Value: trawlCommandLineForDisplay(writer, []string{
				"notes",
				"versions",
				globallyRoutableTrawlLinkText(requestedTrawlLink),
			}),
			ValueIsTrawlCommandAction: true,
		})
	}
	body := ""
	switch openedNoteBody := openedNoteRecord.GetOpenedNoteBody().GetBodyAvailability().(type) {
	case *note.OpenedNoteBody_AvailableNoteBody:
		var moreNoteBodyTextIsOmitted bool
		body, moreNoteBodyTextIsOmitted = openedNoteBodyTextForHumanPresentation(
			openedNoteBody.AvailableNoteBody.GetNoteBodyText(),
		)
		body = strings.TrimSpace(body)
		if moreNoteBodyTextIsOmitted {
			body = strings.TrimSpace(body) + "\n\nMore note text is omitted."
		}
	case *note.OpenedNoteBody_UnavailableNoteBody:
		body = "Note text is unavailable."
	}
	return WriteCard(writer, Card{Title: noteDisplayName, Fields: fields, Body: body})
}

const (
	maximumDisplayedOpenedNoteBodyUnicodeCodePointCount = 1200
	maximumDisplayedOpenedNoteBodyLineCount             = 40
)

func openedNoteBodyTextForHumanPresentation(completeNoteBodyText string) (string, bool) {
	completeNoteBodyUnicodeCodePoints := []rune(completeNoteBodyText)
	displayedNoteBodyUnicodeCodePoints := make(
		[]rune,
		0,
		min(len(completeNoteBodyUnicodeCodePoints), maximumDisplayedOpenedNoteBodyUnicodeCodePointCount),
	)
	displayedLineCount := 1
	for _, unicodeCodePoint := range completeNoteBodyUnicodeCodePoints {
		if len(displayedNoteBodyUnicodeCodePoints) >= maximumDisplayedOpenedNoteBodyUnicodeCodePointCount {
			return string(displayedNoteBodyUnicodeCodePoints), true
		}
		if unicodeCodePoint == '\n' && displayedLineCount >= maximumDisplayedOpenedNoteBodyLineCount {
			return string(displayedNoteBodyUnicodeCodePoints), true
		}
		displayedNoteBodyUnicodeCodePoints = append(displayedNoteBodyUnicodeCodePoints, unicodeCodePoint)
		if unicodeCodePoint == '\n' {
			displayedLineCount++
		}
	}
	return completeNoteBodyText, false
}

func WriteOpenedMessageRecordWithConversationContext(
	writer io.Writer,
	openedMessage *message.OpenedMessageRecordWithConversationContext,
) error {
	if openedMessage == nil {
		return fmt.Errorf("opened message record is missing")
	}
	conversationLink := openedMessage.GetConversationTrawlLink()
	if globallyRoutableTrawlLinkText(conversationLink) == "" {
		return fmt.Errorf("conversation link for opened message is missing")
	}
	conversationDisplayName := strings.TrimSpace(openedMessage.GetConversationDisplayName())
	if _, err := fmt.Fprintln(writer, conversationDisplayName); err != nil {
		return err
	}
	participantDisplayNames := compactDisplayNames(openedMessage.GetConversationParticipantDisplayNames())
	if openedMessageParticipantsAddInformation(conversationDisplayName, participantDisplayNames) {
		if err := WriteWrappedField(
			writer,
			"Participants",
			ConversationParticipantDisplayNamesPreviewForHumanOutput(
				participantDisplayNames,
				uint64(len(participantDisplayNames)),
			),
		); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	contextMessageRecords := openedMessage.GetConversationContextMessageRecordsNewestFirst()
	rows := make([]messageListDisplayRow, 0, len(contextMessageRecords))
	canonicalOpenedMessageRecordReference := openedMessage.GetOpenedMessageRecordReference()
	var mediaForOpenedMessage *message.MessageMedia
	for _, messageRecord := range contextMessageRecords {
		if messageRecord == nil {
			continue
		}
		selected := canonicalArchiveRecordReferencesMatch(
			messageRecord.GetCanonicalRecordReference(),
			canonicalOpenedMessageRecordReference,
		)
		if selected {
			mediaForOpenedMessage = messageRecord.GetMessageMedia()
		}
		rows = append(rows, messageListDisplayRow{
			selected: selected,
			when:     trawlerSpecificCommandAssociatedTime(messageRecord.GetMessageTime()),
			senderDisplayContext: displayedPeopleWithRoles(
				messageRecord.GetPeopleRelatedToMessage(),
				person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
				person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_AUTHOR,
			),
			recipientDisplayContext: displayedPeopleWithRoles(
				messageRecord.GetPeopleRelatedToMessage(),
				person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
			),
			displayedMessageOrMedia: messageTextAndMediaForHumanOutput(
				messageRecord.GetMessageText(),
				messageRecord.GetMessageMedia(),
			),
		})
	}
	if err := writeMessageListRows(writer, rows); err != nil {
		return err
	}
	if err := writeOpenedMessageMedia(writer, mediaForOpenedMessage); err != nil {
		return err
	}
	earlierMessagesOmitted := openedMessage.GetEarlierConversationContextMessagesOmitted()
	laterMessagesOmitted := openedMessage.GetLaterConversationContextMessagesOmitted()
	if !earlierMessagesOmitted && !laterMessagesOmitted {
		return nil
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	omissionMessage := "Earlier messages omitted."
	if earlierMessagesOmitted && laterMessagesOmitted {
		omissionMessage = "Earlier and later messages omitted."
	} else if laterMessagesOmitted {
		omissionMessage = "Later messages omitted."
	}
	if _, err := fmt.Fprintln(writer, omissionMessage); err != nil {
		return err
	}
	return WriteTrawlCommandHint(
		writer,
		"Messages: "+trawlCommandLineForDisplay(
			writer,
			[]string{"messages", "--conversation", globallyRoutableTrawlLinkText(conversationLink)},
		),
	)
}

func writeOpenedMessageMedia(writer io.Writer, media *message.MessageMedia) error {
	if media == nil {
		return nil
	}
	fields := []CardField{
		{Label: "Type", Value: messageMediaContentKindDisplayName(media.GetMessageMediaContentKind())},
		{Label: "Title", Value: strings.TrimSpace(media.GetMessageMediaTitle())},
	}
	if media.MessageMediaByteCount != nil {
		fields = append(fields, CardField{
			Label: "Size",
			Value: FormatInteger(int64(media.GetMessageMediaByteCount())) + " bytes",
		})
	}
	fields = append(fields,
		CardField{Label: "URL", Value: strings.TrimSpace(media.GetMessageMediaHttpsUrl())},
		CardField{Label: "Metadata URL", Value: strings.TrimSpace(media.GetMessageMediaMetadataHttpsUrl())},
	)
	hasValue := false
	for _, field := range fields {
		hasValue = hasValue || strings.TrimSpace(field.Value) != ""
	}
	if !hasValue {
		return nil
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	for _, field := range fields {
		if strings.TrimSpace(field.Value) == "" {
			continue
		}
		if err := WriteWrappedField(writer, field.Label, field.Value); err != nil {
			return err
		}
	}
	return nil
}

func messageMediaContentKindDisplayName(messageMediaContentKind message.MessageMediaContentKind) string {
	switch messageMediaContentKind {
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_ATTACHMENT:
		return "Attachment"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_IMAGE:
		return "Image"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VIDEO:
		return "Video"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_AUDIO:
		return "Audio"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_FILE:
		return "File"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_GIF:
		return "GIF"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_STICKER:
		return "Sticker"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_LINK:
		return "Link"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_PHOTO_OR_VIDEO:
		return "Photo or video"
	case message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VOICE_OR_INSTANT_VIDEO:
		return "Voice message or instant video"
	default:
		return ""
	}
}

func writeConversationRecord(
	writer io.Writer,
	conversationRecord *conversation.ConversationRecord,
	globallyRoutableTrawlLink *identity.GloballyRoutableTrawlLink,
) error {
	if conversationRecord == nil {
		return fmt.Errorf("conversation record is missing")
	}
	participantDisplayNames := conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(
		conversationRecord.GetConversationParticipantIdentitiesObservedByTrawlerArchive(),
	)
	fields := []CardField{{
		Label: "People",
		Value: conversationParticipantDisplayNamesWithUnavailableCount(
			participantDisplayNames,
			conversationRecord.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
		),
	}}
	if mostRecentActivityTime := conversationRecord.GetMostRecentConversationActivityTime(); mostRecentActivityTime != nil && mostRecentActivityTime.IsValid() {
		fields = append(fields, CardField{Label: "Last message", Value: ShortLocalTime(mostRecentActivityTime.AsTime())})
	}
	if conversationRecord.UnreadMessageCount != nil {
		fields = append(fields, CardField{Label: "Unread", Value: FormatInteger(int64(conversationRecord.GetUnreadMessageCount()))})
	}
	globallyRoutableTrawlLinkForHumanOutput := globallyRoutableTrawlLinkText(globallyRoutableTrawlLink)
	if globallyRoutableTrawlLinkForHumanOutput != "" {
		fields = append(fields, CardField{Label: "Link", Value: globallyRoutableTrawlLinkForHumanOutput})
	}
	var hints []string
	if globallyRoutableTrawlLinkForHumanOutput != "" {
		hints = []string{"Messages: " + trawlCommandLineForDisplay(
			writer,
			[]string{"messages", "--conversation", globallyRoutableTrawlLinkForHumanOutput},
		)}
	}
	return WriteCard(writer, Card{
		Title:  strings.TrimSpace(conversationRecord.GetConversationDisplayName()),
		Fields: fields,
		Hints:  hints,
	})
}

func writeCalendarEventRecord(
	writer io.Writer,
	calendarEventRecord *calendarevent.CalendarEventRecord,
	globallyRoutableTrawlLink *identity.GloballyRoutableTrawlLink,
) error {
	if calendarEventRecord == nil {
		return fmt.Errorf("calendar event record is missing")
	}
	fields := []CardField{
		{Label: "Starts", Value: trawlerSpecificCommandAssociatedTime(calendarEventRecord.GetCalendarEventStartTime())},
		{Label: "Ends", Value: trawlerSpecificCommandAssociatedTime(calendarEventRecord.GetCalendarEventEndTime())},
		{Label: "Calendar", Value: strings.TrimSpace(calendarEventRecord.GetCalendarDisplayName())},
		{Label: "Account", Value: strings.TrimSpace(calendarEventRecord.GetCalendarAccountDisplayName())},
		{Label: "Owner or purpose", Value: calendarOwnerOrPurposeDescription(calendarEventRecord.GetCalendarOwnerOrPurposeAnnotation())},
		{Label: "Where", Value: calendarEventPlace(calendarEventRecord.GetCalendarEventLocation())},
		{Label: "People", Value: calendarEventPeople(calendarEventRecord)},
		{Label: "URL", Value: strings.TrimSpace(calendarEventRecord.GetCalendarEventHttpsUrl())},
	}
	if availability := calendarEventAvailabilityForDisplay(calendarEventRecord.GetCalendarEventAvailability()); availability != "" {
		fields = append(fields, CardField{Label: "Availability", Value: availability})
	}
	if status := calendarEventStatusForDisplay(calendarEventRecord.GetCalendarEventStatus()); status != "" {
		fields = append(fields, CardField{Label: "Status", Value: status})
	}
	if calendarEventRecord.GetCalendarEventIsRecurring() {
		fields = append(fields, CardField{Label: "Repeats", Value: "yes"})
	}
	globallyRoutableTrawlLinkForHumanOutput := globallyRoutableTrawlLinkText(globallyRoutableTrawlLink)
	if globallyRoutableTrawlLinkForHumanOutput != "" {
		fields = append(fields, CardField{Label: "Link", Value: globallyRoutableTrawlLinkForHumanOutput})
	}
	return WriteCard(writer, Card{
		Title:  strings.TrimSpace(calendarEventRecord.GetCalendarEventDisplayName()),
		Fields: fields,
		Body:   strings.TrimSpace(calendarEventRecord.GetCalendarEventDescription()),
	})
}

func calendarEventAvailabilityForDisplay(availability calendarevent.CalendarEventAvailability) string {
	switch availability {
	case calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_BUSY:
		return "busy"
	case calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_FREE:
		return "free"
	case calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_TENTATIVE:
		return "tentative"
	case calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNAVAILABLE:
		return "unavailable"
	case calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNKNOWN:
		return "unknown"
	default:
		return ""
	}
}

func calendarEventStatusForDisplay(status calendarevent.CalendarEventStatus) string {
	switch status {
	case calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_CONFIRMED:
		return "confirmed"
	case calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_TENTATIVE:
		return "tentative"
	case calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_CANCELLED:
		return "cancelled"
	case calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_UNKNOWN:
		return "unknown"
	default:
		return ""
	}
}

func compactDisplayNames(displayNames []string) []string {
	compact := make([]string, 0, len(displayNames))
	for _, displayName := range displayNames {
		if displayName = strings.TrimSpace(displayName); displayName != "" {
			compact = append(compact, displayName)
		}
	}
	return compact
}

func openedMessageParticipantsAddInformation(
	conversationDisplayName string,
	participantDisplayNames []string,
) bool {
	if len(participantDisplayNames) == 0 {
		return false
	}
	return len(participantDisplayNames) != 1 ||
		!strings.EqualFold(strings.TrimSpace(conversationDisplayName), participantDisplayNames[0])
}
