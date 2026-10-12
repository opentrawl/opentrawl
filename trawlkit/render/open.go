package render

import (
	"fmt"
	"io"
	"strings"

	calendarrecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar"
	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	note "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
)

type OpenResponseRenderContext struct {
	TrawlerSpecificOpenedRecordPresentationActions TrawlerSpecificCommandActions
}

// WriteOpenedRecord writes one opened record and the commands that go
// deeper.
func (output TrawlCommandOutput) WriteOpenedRecord(
	response *open.OpenResponse,
	context OpenResponseRenderContext,
) error {
	writer := output.standardOutput
	if response == nil {
		return fmt.Errorf("open response is missing")
	}
	if response.GetFailure() != nil {
		return fmt.Errorf("open response is a failure, not a record")
	}
	record := response.GetRecord()
	if record == nil {
		return fmt.Errorf("open response has no record")
	}
	switch typedOpenedRecord := record.GetTypedOpenedRecord().(type) {
	case *open.OpenRecord_OpenedMessageRecordWithConversationContext:
		return writeOpenedMessageRecordWithConversationContext(
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
		return writePersonRecord(
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
	case *open.OpenRecord_CalendarRecord:
		if err := writeCalendarListResponse(
			writer,
			&calendarrecord.CalendarListResponse{CalendarRecordsInDisplayOrder: []*calendarrecord.CalendarRecord{typedOpenedRecord.CalendarRecord}},
			GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference{{
				CanonicalArchiveRecordReference: record.GetCanonicalRecordReference(),
				GloballyRoutableTrawlLink:       response.GetRequestedTrawlLink(),
			}},
		); err != nil {
			return err
		}
		return writeNextCommands(writer, []NextCommand{nextCommandListingCalendarEvents(globallyRoutableTrawlLinkText(response.GetRequestedTrawlLink()))})
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
		return writeTrawlerSpecificCommandDetailPresentation(
			writer,
			trawlerSpecificOpenedRecordPresentation.GetDetailPresentation(),
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			context.TrawlerSpecificOpenedRecordPresentationActions.DetailActionsInDisplayOrder,
			true,
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
	fields := []cardField{
		{label: "Folder", value: strings.TrimSpace(openedNoteRecord.GetNoteFolderDisplayName())},
		{label: "Created", value: formatExactTimeInThisComputerTimeZone(openedNoteRecord.GetNoteCreatedTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord)},
	}
	if openedNoteRecord.GetSpecificRecoveredNoteVersionWasOpened() {
		fields = append(fields, cardField{
			label: "Recovered version",
			value: formatExactTimeInThisComputerTimeZone(openedNoteRecord.GetOpenedNoteVersionTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord),
		})
	} else {
		fields = append(fields, cardField{
			label: "Modified",
			value: formatExactTimeInThisComputerTimeZone(openedNoteRecord.GetNoteModifiedTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord),
		})
	}
	// The note's versions are a next command, not a field: one version is
	// the note itself, so there is nothing more to list.
	var nextCommands []NextCommand
	if openedNoteRecord.GetRecoveredNoteVersionCount() > 1 {
		nextCommands = append(nextCommands, nextCommandWithLabel(
			nextCommandLabelNoteVersions,
			"notes", "versions", globallyRoutableTrawlLinkText(requestedTrawlLink),
		))
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
	return writeCard(writer, card{title: noteDisplayName, fields: fields, body: body, nextCommands: nextCommands})
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

func writeOpenedMessageRecordWithConversationContext(
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
	if err := writeLines(writer, Wrap(conversationDisplayName, OutputWidth(writer))); err != nil {
		return err
	}
	participantDisplayNames := compactDisplayNames(openedMessage.GetConversationParticipantDisplayNames())
	if openedMessageParticipantsAddInformation(conversationDisplayName, participantDisplayNames) {
		if err := writeWrappedField(
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
	contextMessageRecords := openedMessage.GetConversationContextMessageRecordsNewestFirst()
	canonicalOpenedMessageRecordReference := openedMessage.GetOpenedMessageRecordReference()
	for _, messageRecord := range contextMessageRecords {
		if !canonicalArchiveRecordReferencesMatch(messageRecord.GetCanonicalRecordReference(), canonicalOpenedMessageRecordReference) {
			continue
		}
		openedMessageTime := formatArchiveRecordAssociatedTime(
			messageRecord.GetMessageTime(),
			localTimeWithUTCOffsetAndUTCForOpenedRecord,
		)
		if openedMessageTime != "" {
			if err := writeWrappedField(writer, "Sent", openedMessageTime); err != nil {
				return err
			}
		}
		break
	}
	if err := writeLine(writer, ""); err != nil {
		return err
	}
	rows := make([]messageListDisplayRow, 0, len(contextMessageRecords))
	var mediaForOpenedMessage *message.MessageMedia
	for _, messageRecord := range contextMessageRecords {
		if messageRecord == nil {
			continue
		}
		openedMessageIsThisRecord := canonicalArchiveRecordReferencesMatch(
			messageRecord.GetCanonicalRecordReference(),
			canonicalOpenedMessageRecordReference,
		)
		if openedMessageIsThisRecord {
			mediaForOpenedMessage = messageRecord.GetMessageMedia()
		}
		rows = append(rows, messageListDisplayRow{
			openedMessage: openedMessageIsThisRecord,
			when:          formatArchiveRecordAssociatedTime(messageRecord.GetMessageTime(), localTimeWithUTCOffsetForListRow),
			who:           formatSendersAndRecipients(messageRecord.GetPeopleRelatedToMessage()),
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
	omissionSentence := ""
	switch {
	case earlierMessagesOmitted && laterMessagesOmitted:
		omissionSentence = "Earlier and later messages are not shown."
	case earlierMessagesOmitted:
		omissionSentence = "Earlier messages are not shown."
	case laterMessagesOmitted:
		omissionSentence = "Later messages are not shown."
	}
	if omissionSentence != "" {
		if err := writeLines(writer, []string{"", omissionSentence}); err != nil {
			return err
		}
	}
	return writeNextCommands(writer, []NextCommand{
		nextCommandListingConversationMessages(globallyRoutableTrawlLinkText(conversationLink)),
	})
}

func writeOpenedMessageMedia(writer io.Writer, media *message.MessageMedia) error {
	if media == nil {
		return nil
	}
	fields := []cardField{
		{label: "Type", value: messageMediaContentKindDisplayName(media.GetMessageMediaContentKind())},
		{label: "Title", value: strings.TrimSpace(media.GetMessageMediaTitle())},
	}
	if media.MessageMediaByteCount != nil {
		fields = append(fields, cardField{label: "Size", value: formatByteCount(media.GetMessageMediaByteCount())})
	}
	fields = append(fields,
		cardField{label: "URL", value: strings.TrimSpace(media.GetMessageMediaHttpsUrl())},
		cardField{label: "Metadata URL", value: strings.TrimSpace(media.GetMessageMediaMetadataHttpsUrl())},
	)
	hasValue := false
	for _, field := range fields {
		hasValue = hasValue || strings.TrimSpace(field.value) != ""
	}
	if !hasValue {
		return nil
	}
	if err := writeLine(writer, ""); err != nil {
		return err
	}
	return writeCard(writer, card{fields: fields})
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
	numberOfConversationParticipantsForHumanOutput :=
		resolveNumberOfDistinctConversationParticipantRecordsForHumanOutput(
			participantDisplayNames,
			conversationRecord.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
		)
	fields := []cardField{{
		label: "People",
		value: conversationParticipantDisplayNamesForOpenedConversation(
			writer,
			participantDisplayNames,
			numberOfConversationParticipantsForHumanOutput,
		),
	}}
	fields = append(fields, cardField{
		label: "Last message",
		value: formatExactTimeInThisComputerTimeZone(conversationRecord.GetMostRecentConversationActivityTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord),
	})
	if conversationRecord.UnreadMessageCount != nil {
		fields = append(fields, cardField{label: "Unread", value: FormatInteger(int64(conversationRecord.GetUnreadMessageCount()))})
	}
	globallyRoutableTrawlLinkForHumanOutput := globallyRoutableTrawlLinkText(globallyRoutableTrawlLink)
	var nextCommands []NextCommand
	if globallyRoutableTrawlLinkForHumanOutput != "" {
		nextCommands = append(nextCommands, nextCommandListingConversationMessages(globallyRoutableTrawlLinkForHumanOutput))
		if numberOfConversationParticipantsForHumanOutput > 0 {
			nextCommands = append(nextCommands, nextCommandWithLabel(
				nextCommandLabelConversationParticipants,
				"open", globallyRoutableTrawlLinkForHumanOutput, "--participants",
			))
		}
	}
	return writeCard(writer, card{
		title:        strings.TrimSpace(conversationRecord.GetConversationDisplayName()),
		fields:       fields,
		nextCommands: nextCommands,
	})
}

const maximumOpenedConversationParticipantPreviewLineCount = 6

func conversationParticipantDisplayNamesForOpenedConversation(
	writer io.Writer,
	participantDisplayNames []string,
	numberOfConversationParticipantsForHumanOutput uint64,
) string {
	numberOfParticipantDisplayNamesToShow := len(participantDisplayNames)
	for {
		preview := conversationParticipantDisplayNamesAndHiddenCount(
			participantDisplayNames,
			numberOfParticipantDisplayNamesToShow,
			numberOfConversationParticipantsForHumanOutput,
		)
		wrappedPreview := WrapWithIndent("People: ", preview, OutputWidth(writer), "")
		if len(wrappedPreview) <= maximumOpenedConversationParticipantPreviewLineCount ||
			numberOfParticipantDisplayNamesToShow == 0 {
			return preview
		}
		numberOfParticipantDisplayNamesToShow--
	}
}

func writeCalendarEventRecord(
	writer io.Writer,
	calendarEventRecord *calendarevent.CalendarEventRecord,
	globallyRoutableTrawlLink *identity.GloballyRoutableTrawlLink,
) error {
	if calendarEventRecord == nil {
		return fmt.Errorf("calendar event record is missing")
	}
	fields := []cardField{
		{label: "When", value: formatArchiveRecordAssociatedTimeRange(
			calendarEventRecord.GetCalendarEventStartTime(),
			calendarEventRecord.GetCalendarEventEndTime(),
			localTimeWithUTCOffsetAndUTCForOpenedRecord,
		)},
		{label: "Calendar", value: strings.TrimSpace(calendarEventRecord.GetCalendarDisplayName())},
		{label: "Account", value: strings.TrimSpace(calendarEventRecord.GetCalendarAccountDisplayName())},
		{label: "Owner or purpose", value: calendarOwnerOrPurposeDescription(calendarEventRecord.GetCalendarOwnerOrPurposeAnnotation())},
		{label: "Where", value: calendarEventPlace(calendarEventRecord.GetCalendarEventLocation())},
		{label: "People", value: calendarEventPeople(calendarEventRecord)},
		{label: "URL", value: strings.TrimSpace(calendarEventRecord.GetCalendarEventHttpsUrl())},
		{label: "Availability", value: calendarEventAvailabilityForDisplay(calendarEventRecord.GetCalendarEventAvailability())},
		{label: "Status", value: calendarEventStatusForDisplay(calendarEventRecord.GetCalendarEventStatus())},
	}
	if calendarEventRecord.GetCalendarEventIsRecurring() {
		fields = append(fields, cardField{label: "Repeats", value: "yes"})
	}
	return writeCard(writer, card{
		title:  strings.TrimSpace(calendarEventRecord.GetCalendarEventDisplayName()),
		fields: fields,
		body:   strings.TrimSpace(calendarEventRecord.GetCalendarEventDescription()),
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
