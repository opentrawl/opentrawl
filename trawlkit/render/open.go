package render

import (
	"fmt"
	"io"
	"strings"

	calendareventv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event/v1"
	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
)

func WriteOpenResponse(writer io.Writer, response *openv1.OpenResponse) error {
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
	case *openv1.OpenRecord_OpenedMessageRecordWithConversationContext:
		return WriteOpenedMessageRecordWithConversationContext(
			writer,
			typedOpenedRecord.OpenedMessageRecordWithConversationContext,
		)
	case *openv1.OpenRecord_ConversationRecord:
		return writeConversationRecord(
			writer,
			typedOpenedRecord.ConversationRecord,
			response.GetRequestedGloballyRoutableTrawlLink(),
		)
	case *openv1.OpenRecord_PersonRecord:
		return WritePersonRecord(
			writer,
			typedOpenedRecord.PersonRecord,
			response.GetRequestedGloballyRoutableTrawlLink(),
		)
	case *openv1.OpenRecord_CalendarEventRecord:
		return writeCalendarEventRecord(
			writer,
			typedOpenedRecord.CalendarEventRecord,
			response.GetRequestedGloballyRoutableTrawlLink(),
		)
	case *openv1.OpenRecord_TrawlerSpecificOpenedRecord:
		trawlerSpecificOpenedRecord := typedOpenedRecord.TrawlerSpecificOpenedRecord
		if trawlerSpecificOpenedRecord == nil {
			return fmt.Errorf("trawler-specific opened record is missing")
		}
		globallyRoutableTrawlLinksByCanonicalRecordReference := map[string]string{
			strings.TrimSpace(record.GetCanonicalOpenedRecordReference()): strings.TrimSpace(
				response.GetRequestedGloballyRoutableTrawlLink(),
			),
		}
		return WriteTrawlerSpecificCommandDetailPresentation(
			writer,
			trawlerSpecificOpenedRecord.GetTrawlerSpecificOpenedRecordDetailPresentation(),
			globallyRoutableTrawlLinksByCanonicalRecordReference,
		)
	default:
		return fmt.Errorf("open record has no typed record")
	}
}

func WriteOpenedMessageRecordWithConversationContext(
	writer io.Writer,
	openedMessage *messagev1.OpenedMessageRecordWithConversationContext,
) error {
	if openedMessage == nil {
		return fmt.Errorf("opened message record is missing")
	}
	conversationLink := strings.TrimSpace(
		openedMessage.GetGloballyRoutableTrawlLinkForConversationContainingOpenedMessage(),
	)
	if conversationLink == "" {
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
	contextMessageRecords := openedMessage.GetConversationContextMessageRecordsInDisplayOrder()
	rows := make([][]string, 0, len(contextMessageRecords))
	canonicalOpenedMessageRecordReference := strings.TrimSpace(
		openedMessage.GetCanonicalOpenedMessageRecordReference(),
	)
	maximumSurroundingMessageTextDisplayWidth := max(OutputWidth(writer)*2, 80)
	for _, messageRecord := range contextMessageRecords {
		if messageRecord == nil {
			continue
		}
		timeDisplay := trawlerSpecificCommandAssociatedTime(messageRecord.GetMessageTime())
		messageText := messageRecord.GetDisplayedMessageOrMediaText()
		if strings.TrimSpace(
			messageRecord.GetCanonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		) == canonicalOpenedMessageRecordReference {
			timeDisplay = strings.TrimSpace("→ " + timeDisplay)
		} else {
			messageText = Truncate(messageText, maximumSurroundingMessageTextDisplayWidth)
		}
		rows = append(rows, []string{
			timeDisplay,
			displayedPeopleWithRoles(
				messageRecord.GetPeopleRelatedToMessage(),
				personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
				personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_AUTHOR,
			),
			messageText,
		})
	}
	if err := WriteTable(writer, []TableColumn{
		{Header: "time", MinimumWidth: 16},
		{Header: "from", Wrap: true, MaximumWrappedLines: 2},
		{Header: "text", Wrap: true},
	}, rows); err != nil {
		return err
	}
	if err := writeOpenedMessageMedia(writer, openedMessage.GetOpenedMessageMedia()); err != nil {
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
	_, err := fmt.Fprintf(
		writer,
		"Messages: %s messages --conversation %s\n",
		TrawlInvocationDisplay(writer),
		conversationLink,
	)
	return err
}

func writeOpenedMessageMedia(writer io.Writer, media *messagev1.MessageMedia) error {
	if media == nil {
		return nil
	}
	fields := []CardField{
		{Label: "Type", Value: strings.TrimSpace(media.GetMessageMediaKind())},
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

func writeConversationRecord(
	writer io.Writer,
	conversationRecord *conversationv1.ConversationRecord,
	globallyRoutableTrawlLink string,
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
	globallyRoutableTrawlLink = strings.TrimSpace(globallyRoutableTrawlLink)
	if globallyRoutableTrawlLink != "" {
		fields = append(fields, CardField{Label: "Link", Value: globallyRoutableTrawlLink})
	}
	var hints []string
	if globallyRoutableTrawlLink != "" {
		hints = []string{"Messages: " + trawlCommandLineForDisplay(
			writer,
			[]string{"messages", "--conversation", globallyRoutableTrawlLink},
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
	calendarEventRecord *calendareventv1.CalendarEventRecord,
	globallyRoutableTrawlLink string,
) error {
	if calendarEventRecord == nil {
		return fmt.Errorf("calendar event record is missing")
	}
	fields := []CardField{
		{Label: "Starts", Value: trawlerSpecificCommandAssociatedTime(calendarEventRecord.GetCalendarEventStartTime())},
		{Label: "Ends", Value: trawlerSpecificCommandAssociatedTime(calendarEventRecord.GetCalendarEventEndTime())},
		{Label: "Calendar", Value: strings.TrimSpace(calendarEventRecord.GetCalendarDisplayName())},
		{Label: "Account", Value: strings.TrimSpace(calendarEventRecord.GetCalendarAccountDisplayName())},
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
	globallyRoutableTrawlLink = strings.TrimSpace(globallyRoutableTrawlLink)
	if globallyRoutableTrawlLink != "" {
		fields = append(fields, CardField{Label: "Link", Value: globallyRoutableTrawlLink})
	}
	return WriteCard(writer, Card{
		Title:  strings.TrimSpace(calendarEventRecord.GetCalendarEventDisplayName()),
		Fields: fields,
		Body:   strings.TrimSpace(calendarEventRecord.GetCalendarEventDescription()),
	})
}

func calendarEventAvailabilityForDisplay(availability calendareventv1.CalendarEventAvailability) string {
	switch availability {
	case calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_BUSY:
		return "busy"
	case calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_FREE:
		return "free"
	case calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_TENTATIVE:
		return "tentative"
	case calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNAVAILABLE:
		return "unavailable"
	case calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNKNOWN:
		return "unknown"
	default:
		return ""
	}
}

func calendarEventStatusForDisplay(status calendareventv1.CalendarEventStatus) string {
	switch status {
	case calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_CONFIRMED:
		return "confirmed"
	case calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_TENTATIVE:
		return "tentative"
	case calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_CANCELLED:
		return "cancelled"
	case calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_UNKNOWN:
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
