package render

import (
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"

	email "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/email"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	emailSubjectAnchorIdentifier = "subject"
	emailBodyAnchorIdentifier    = "body"
	emailMissingSubjectText      = "(no subject)"
)

type openedEmailDisplayField struct {
	label            string
	textValue        string
	exactTimeValue   *timestamppb.Timestamp
	anchorIdentifier string
}

func writeOpenedEmailMessageRecord(writer io.Writer, record *email.OpenedEmailMessageRecord) error {
	if record == nil {
		return fmt.Errorf("opened email message record is missing")
	}
	title, rawFields, body := openedEmailDisplayParts(record)
	fields := make([]cardField, 0, len(rawFields))
	for _, field := range rawFields {
		value := field.textValue
		if field.exactTimeValue != nil {
			value = formatExactTimeInThisComputerTimeZone(field.exactTimeValue, localTimeWithUTCOffsetAndUTCForOpenedRecord)
		}
		fields = append(fields, cardField{label: field.label, value: value})
	}
	return writeCard(writer, card{
		title:  title,
		fields: fields,
		body:   body,
	})
}

// EmailOpenedRecordPresentation gives app clients the same field names,
// values, anchors and order as the shared opened-email CLI renderer.
func EmailOpenedRecordPresentation(record *email.OpenedEmailMessageRecord) *open.TrawlerSpecificOpenedRecordPresentation {
	title, rawFields, body := openedEmailDisplayParts(record)
	fields := make([]*presentation.TrawlerSpecificCommandDetailPresentationField, 0, len(rawFields))
	for _, field := range rawFields {
		var presentationField *presentation.TrawlerSpecificCommandDetailPresentationField
		if field.exactTimeValue != nil {
			presentationField = emailPresentationExactTimeField(field.label, field.exactTimeValue.AsTime())
		} else {
			presentationField = emailPresentationTextField(field.label, field.textValue, field.anchorIdentifier)
		}
		fields = append(fields, presentationField)
	}
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       title,
		DetailDisplayNameAnchor: &identity.RecordAnchorIdentifier{RecordAnchorIdentifier: emailSubjectAnchorIdentifier},
		FieldsInDisplayOrder:    fields,
	}
	if body != "" {
		detail.Body = &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: body}
		detail.BodyAnchor = &identity.RecordAnchorIdentifier{RecordAnchorIdentifier: emailBodyAnchorIdentifier}
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{DetailPresentation: detail}
}

func openedEmailDisplayParts(record *email.OpenedEmailMessageRecord) (string, []openedEmailDisplayField, string) {
	headers := record.GetEmailMessageHeaders()
	title := strings.TrimSpace(headers.GetEmailSubject())
	if title == "" {
		title = emailMissingSubjectText
	}
	fields := make([]openedEmailDisplayField, 0, 10+len(record.GetEmailMessageAttachments()))
	addTextField := func(label, value, anchorIdentifier string) {
		if value = strings.TrimSpace(value); value != "" {
			fields = append(fields, openedEmailDisplayField{
				label:            label,
				textValue:        value,
				anchorIdentifier: anchorIdentifier,
			})
		}
	}
	addTextField("From", formatEmailSenderForDisplay(headers.GetSenderDisplayName(), headers.GetSenderEmailAddress()), "")
	addTextField("To", headers.GetRecipientEmailAddresses(), "")
	addTextField("Cc", headers.GetCopiedRecipientEmailAddresses(), "")
	if replyToText := strings.TrimSpace(headers.GetReplyToEmailAddresses()); replyToText != "" {
		if !replyToMatchesSingleSenderAddress(replyToText, headers.GetSenderEmailAddress()) {
			addTextField("Reply-To", replyToText, "")
		}
	}
	if messageTime := record.GetEmailMessageTime(); messageTime != nil {
		fields = append(fields, openedEmailDisplayField{label: "Date", exactTimeValue: messageTime})
	}
	deletedLabel := "Deleted from source"
	switch sourceDetails := record.GetEmailSourceDetails().(type) {
	case *email.OpenedEmailMessageRecord_GmailEmailSourceDetails:
		deletedLabel = "Deleted from Gmail"
		addTextField("Labels", strings.Join(compactDisplayNames(sourceDetails.GmailEmailSourceDetails.GetGmailLabelNames()), ", "), "")
	case *email.OpenedEmailMessageRecord_AppleMailEmailSourceDetails:
		deletedLabel = "Deleted from Mail"
		addTextField("Account", sourceDetails.AppleMailEmailSourceDetails.GetAppleMailAccountDisplayName(), "")
		addTextField("Mailbox", sourceDetails.AppleMailEmailSourceDetails.GetAppleMailMailboxName(), "")
	}
	addTextField("List", headers.GetMailingListIdentifier(), "")
	if record.GetEmailMessageIsUnread() {
		addTextField("Unread", "Yes", "")
	}
	if deletedTime := record.GetDeletedFromEmailSourceTime(); deletedTime != nil {
		fields = append(fields, openedEmailDisplayField{label: deletedLabel, exactTimeValue: deletedTime})
	}
	for index, attachment := range record.GetEmailMessageAttachments() {
		parts := compactDisplayNames([]string{attachment.GetAttachmentFilename(), attachment.GetAttachmentMediaType()})
		if attachment.AttachmentByteCount != nil && attachment.GetAttachmentByteCount() >= 0 {
			parts = append(parts, formatByteCount(uint64(attachment.GetAttachmentByteCount())))
		}
		addTextField("Attachment", strings.Join(parts, " · "), "attachment-"+strconv.Itoa(index+1))
	}
	return title, fields, strings.TrimSpace(record.GetEmailMessageBodyText())
}

func replyToMatchesSingleSenderAddress(replyToHeaderText, senderEmailAddress string) bool {
	senderEmailAddress = strings.ToLower(strings.TrimSpace(senderEmailAddress))
	if senderEmailAddress == "" {
		return false
	}
	parsedAddresses, err := mail.ParseAddressList(replyToHeaderText)
	if err != nil || len(parsedAddresses) != 1 {
		return strings.EqualFold(strings.TrimSpace(replyToHeaderText), senderEmailAddress)
	}
	return strings.EqualFold(strings.TrimSpace(parsedAddresses[0].Address), senderEmailAddress)
}

func formatEmailSenderForDisplay(displayName, emailAddress string) string {
	displayName = strings.TrimSpace(displayName)
	emailAddress = strings.TrimSpace(emailAddress)
	switch {
	case displayName != "" && emailAddress != "":
		return displayName + " <" + emailAddress + ">"
	case displayName != "":
		return displayName
	default:
		return emailAddress
	}
}

func emailPresentationTextField(label, value, anchorIdentifier string) *presentation.TrawlerSpecificCommandDetailPresentationField {
	field := &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: label,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: value},
		},
	}
	if anchorIdentifier != "" {
		field.FieldAnchor = &identity.RecordAnchorIdentifier{RecordAnchorIdentifier: anchorIdentifier}
	}
	return field
}

func emailPresentationExactTimeField(label string, exactTime time.Time) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: label,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentation.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
						ExactTime: timestamppb.New(exactTime),
					},
				},
			},
		},
	}
}
