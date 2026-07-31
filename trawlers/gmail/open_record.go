package gmail

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	gmailopen "github.com/opentrawl/opentrawl/gmail/proto/trawl/gmail/open"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*open.OpenRecord, error) {
	value, err := c.loadOpenMessage(ctx, req, localShortReference)
	if err != nil {
		return nil, err
	}
	openedGmailMessageRecord, err := projectOpenRecord(value)
	if err != nil {
		return nil, err
	}
	record := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedGmailMessageRecord.GetCanonicalGmailMessageRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
			TrawlerSpecificOpenedRecordPresentation: &open.TrawlerSpecificOpenedRecordPresentation{
				DetailPresentation: projectOpenDetailPresentation(openedGmailMessageRecord),
			},
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func projectOpenRecord(value archive.OpenResult) (*gmailopen.OpenedGmailMessageRecord, error) {
	record := &gmailopen.OpenedGmailMessageRecord{
		CanonicalGmailMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(value.Ref),
		GmailMessageIdentifier:               value.ID,
		GmailThreadIdentifier:                value.ThreadID,
		GmailMessageHeaders: &gmailopen.OpenedGmailMessageHeaders{
			RecipientEmailAddresses: value.Headers.ToAddress,
			GmailMessageSubject:     value.Headers.Subject,
		},
		GmailLabelNames:                 append([]string(nil), value.Labels...),
		GmailMessageIsUnread:            value.Unread,
		GmailMessageAttachments:         make([]*gmailopen.OpenedGmailMessageAttachment, 0, len(value.Attachments)),
		GmailMessageBodyText:            value.Body,
		GmailMessageBodyTextIsTruncated: value.BodyTruncated,
	}
	if messageTimeText := strings.TrimSpace(value.Time); messageTimeText != "" {
		messageTime, err := time.Parse(time.RFC3339Nano, messageTimeText)
		if err != nil {
			return nil, fmt.Errorf("invalid Gmail internal message time with RFC822 Date header fallback %q: %w", messageTimeText, err)
		}
		record.GmailInternalMessageTimeWithRfc822DateHeaderFallback = timestamppb.New(messageTime)
	}
	setOptionalString(&record.GmailMessageHeaders.SenderDisplayName, value.Headers.FromName)
	setOptionalString(&record.GmailMessageHeaders.SenderEmailAddress, value.Headers.FromAddress)
	setOptionalString(&record.GmailMessageHeaders.CopiedRecipientEmailAddresses, value.Headers.CcAddress)
	for _, attachment := range value.Attachments {
		record.GmailMessageAttachments = append(record.GmailMessageAttachments, &gmailopen.OpenedGmailMessageAttachment{
			AttachmentFilename:  attachment.Filename,
			AttachmentMediaType: attachment.MIMEType,
			AttachmentByteCount: attachment.Size,
		})
	}
	if value.BodyElidedChars != 0 {
		elided := int64(value.BodyElidedChars)
		record.OmittedGmailMessageBodyCharacterCount = &elided
	}
	return record, nil
}

func setOptionalString(target **string, value string) {
	if value != "" {
		*target = &value
	}
}

func projectOpenDetailPresentation(record *gmailopen.OpenedGmailMessageRecord) *presentationcontract.TrawlerSpecificCommandDetailPresentation {
	title := strings.TrimSpace(record.GmailMessageHeaders.GmailMessageSubject)
	if title == "" {
		title = "(no subject)"
	}
	fields := make([]*presentationcontract.TrawlerSpecificCommandDetailPresentationField, 0, 6+len(record.GmailMessageAttachments))
	if from := formatPresentationAddress(record.GmailMessageHeaders.GetSenderDisplayName(), record.GmailMessageHeaders.GetSenderEmailAddress()); from != "" {
		fields = append(fields, gmailDetailTextField("From", from, ""))
	}
	if value := strings.TrimSpace(record.GmailMessageHeaders.RecipientEmailAddresses); value != "" {
		fields = append(fields, gmailDetailTextField("To", value, ""))
	}
	if value := strings.TrimSpace(record.GmailMessageHeaders.GetCopiedRecipientEmailAddresses()); value != "" {
		fields = append(fields, gmailDetailTextField("Cc", value, ""))
	}
	if messageTime := record.GetGmailInternalMessageTimeWithRfc822DateHeaderFallback(); messageTime != nil {
		fields = append(fields, gmailDetailExactTimeField("Date", messageTime.AsTime()))
	}
	if labels := joinPresentationStrings(record.GmailLabelNames); labels != "" {
		fields = append(fields, gmailDetailTextField("Labels", labels, ""))
	}
	fields = append(fields, gmailDetailTextField("Unread", formatPresentationBool(record.GmailMessageIsUnread), ""))
	for index, attachment := range record.GmailMessageAttachments {
		attachmentDescription := strings.Join(compactPresentationValues(
			attachment.AttachmentFilename,
			attachment.AttachmentMediaType,
			presentation.Bytes(attachment.AttachmentByteCount),
		), " · ")
		fields = append(fields, gmailDetailTextField("Attachment", attachmentDescription, attachmentAnchorID(index)))
	}
	detail := &presentationcontract.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       title,
		DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier("subject"),
		FieldsInDisplayOrder:    fields,
	}
	if body := strings.TrimSpace(record.GmailMessageBodyText); body != "" {
		detail.Body = &presentationcontract.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: body}
		detail.BodyAnchor = trawlkit.NewRecordAnchorIdentifier("body")
	}
	if record.GmailMessageBodyTextIsTruncated {
		detail.FieldsInDisplayOrder = append(detail.FieldsInDisplayOrder,
			gmailDetailTextField("Body", fmt.Sprintf("%d characters omitted", record.GetOmittedGmailMessageBodyCharacterCount()), ""),
		)
	}
	return detail
}

func gmailDetailTextField(fieldDisplayName, textValue, fixedAnchorIdentifier string) *presentationcontract.TrawlerSpecificCommandDetailPresentationField {
	field := &presentationcontract.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationcontract.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationcontract.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
		},
	}
	if fixedAnchorIdentifier != "" {
		field.FieldAnchor = trawlkit.NewRecordAnchorIdentifier(fixedAnchorIdentifier)
	}
	return field
}

func gmailDetailExactTimeField(fieldDisplayName string, exactTime time.Time) *presentationcontract.TrawlerSpecificCommandDetailPresentationField {
	return &presentationcontract.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationcontract.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationcontract.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentationcontract.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationcontract.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
						ExactTime: timestamppb.New(exactTime),
					},
				},
			},
		},
	}
}

func compactPresentationValues(values ...string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			compacted = append(compacted, value)
		}
	}
	return compacted
}

func attachmentAnchorID(index int) string {
	return fmt.Sprintf("attachment-%d", index+1)
}

func formatPresentationAddress(name, address string) string {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if name != "" && address != "" {
		return name + " <" + address + ">"
	}
	if name != "" {
		return name
	}
	return address
}

func joinPresentationStrings(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return strings.Join(items, ", ")
}

func formatPresentationBool(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}
