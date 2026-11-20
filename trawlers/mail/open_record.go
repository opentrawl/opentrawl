package mail

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/mail/internal/archive"
	mailopen "github.com/opentrawl/opentrawl/trawlers/mail/proto/trawl/mail/open"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	presentationformat "github.com/opentrawl/opentrawl/trawlkit/presentation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OpenRecord returns the whole message body. A message Apple Mail no longer
// has still opens, with the time it left Apple Mail.
func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*open.OpenRecord, error) {
	archiveStore, err := archive.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveUnavailableError(err)
	}
	matches, err := req.ResolveShortReference(ctx, localShortReference)
	switch {
	case errors.Is(err, trawlkit.ErrUnknownShortRef):
		return nil, newMailCommandError("unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage).Error(), err)
	case errors.Is(err, trawlkit.ErrAmbiguousShortRef):
		return nil, newMailCommandError("ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounMessage).Error(), err)
	case err != nil:
		return nil, err
	}
	opened, err := archiveStore.OpenMessage(ctx, trawlkit.CanonicalArchiveRecordReferenceText(matches[0]))
	if err != nil {
		return nil, newMailCommandError("message_not_found", "message could not be opened", err)
	}
	openedRecord := openedAppleMailMessageRecord(opened)
	record := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedRecord.GetCanonicalAppleMailMessageRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
			TrawlerSpecificOpenedRecordPresentation: &open.TrawlerSpecificOpenedRecordPresentation{
				DetailPresentation: openedAppleMailMessageDetailPresentation(openedRecord),
			},
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func openedAppleMailMessageRecord(opened archive.OpenedMailMessage) *mailopen.OpenedAppleMailMessageRecord {
	record := &mailopen.OpenedAppleMailMessageRecord{
		CanonicalAppleMailMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(opened.MailMessageRecordReference),
		AppleMailAccountDisplayName:              opened.AppleMailAccountDisplayName,
		AppleMailMailboxName:                     opened.AppleMailMailboxName,
		AppleMailMessageHeaders: &mailopen.OpenedAppleMailMessageHeaders{
			SenderDisplayName:             optionalText(opened.SenderDisplayName),
			SenderEmailAddress:            optionalText(opened.SenderEmailAddress),
			RecipientEmailAddresses:       opened.RecipientEmailAddresses,
			CopiedRecipientEmailAddresses: optionalText(opened.CopiedRecipientEmailAddresses),
			ReplyToEmailAddresses:         optionalText(opened.ReplyToEmailAddresses),
			MailingListIdentifier:         optionalText(opened.MailingListIdentifier),
			Rfc5322MessageIdentifier:      optionalText(opened.RFC5322MessageIdentifier),
			InReplyToMessageIdentifiers:   optionalText(opened.InReplyToMessageIdentifiers),
			EmailSubject:                  opened.EmailSubject,
		},
		AppleMailMessageIsUnread: opened.MessageIsUnread,
		AppleMailMessageBodyText: opened.EmailBodyText,
	}
	if !opened.MessageReceivedTime.IsZero() {
		record.AppleMailMessageReceivedTime = timestamppb.New(opened.MessageReceivedTime)
	}
	if !opened.DeletedFromAppleMailTime.IsZero() {
		record.AppleMailMessageDeletedFromAppleMailTime = timestamppb.New(opened.DeletedFromAppleMailTime)
	}
	for _, attachment := range opened.ArchivedMailAttachments {
		record.AppleMailMessageAttachments = append(record.AppleMailMessageAttachments, &mailopen.OpenedAppleMailMessageAttachment{
			AttachmentFilename:  attachment.AttachmentFilename,
			AttachmentMediaType: attachment.AttachmentMediaType,
			AttachmentByteCount: attachment.AttachmentByteCount,
		})
	}
	return record
}

func openedAppleMailMessageDetailPresentation(record *mailopen.OpenedAppleMailMessageRecord) *presentation.TrawlerSpecificCommandDetailPresentation {
	headers := record.GetAppleMailMessageHeaders()
	title := strings.TrimSpace(headers.GetEmailSubject())
	if title == "" {
		title = "(no subject)"
	}
	var fields []*presentation.TrawlerSpecificCommandDetailPresentationField
	addTextField := func(fieldDisplayName, textValue, anchorIdentifier string) {
		if textValue = strings.TrimSpace(textValue); textValue != "" {
			fields = append(fields, mailDetailTextField(fieldDisplayName, textValue, anchorIdentifier))
		}
	}
	addTextField("From", formatSender(headers.GetSenderDisplayName(), headers.GetSenderEmailAddress()), "")
	addTextField("To", headers.GetRecipientEmailAddresses(), "")
	addTextField("Cc", headers.GetCopiedRecipientEmailAddresses(), "")
	if replyTo := mailparse.ParseEmailAddressList(headers.GetReplyToEmailAddresses()); len(replyTo) != 1 ||
		string(replyTo[0].NormalizedEmailAddress) != headers.GetSenderEmailAddress() {
		addTextField("Reply-To", headers.GetReplyToEmailAddresses(), "")
	}
	if receivedTime := record.GetAppleMailMessageReceivedTime(); receivedTime != nil {
		fields = append(fields, mailDetailExactTimeField("Date", receivedTime.AsTime()))
	}
	addTextField("Account", record.GetAppleMailAccountDisplayName(), "")
	addTextField("Mailbox", record.GetAppleMailMailboxName(), "")
	addTextField("List", headers.GetMailingListIdentifier(), "")
	if record.GetAppleMailMessageIsUnread() {
		addTextField("Unread", "Yes", "")
	}
	if deletedTime := record.GetAppleMailMessageDeletedFromAppleMailTime(); deletedTime != nil {
		fields = append(fields, mailDetailExactTimeField("Deleted from Mail", deletedTime.AsTime()))
	}
	for index, attachment := range record.GetAppleMailMessageAttachments() {
		values := compactTexts(attachment.GetAttachmentFilename(), attachment.GetAttachmentMediaType())
		if attachment.AttachmentByteCount != nil {
			values = append(values, presentationformat.Bytes(attachment.GetAttachmentByteCount()))
		}
		addTextField("Attachment", strings.Join(values, " · "), "attachment-"+strconv.Itoa(index+1))
	}
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       title,
		DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier("subject"),
		FieldsInDisplayOrder:    fields,
	}
	if body := strings.TrimSpace(record.GetAppleMailMessageBodyText()); body != "" {
		detail.Body = &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: body}
		detail.BodyAnchor = trawlkit.NewRecordAnchorIdentifier("body")
	}
	return detail
}

func mailDetailTextField(fieldDisplayName, textValue, anchorIdentifier string) *presentation.TrawlerSpecificCommandDetailPresentationField {
	field := &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
		},
	}
	if anchorIdentifier != "" {
		field.FieldAnchor = trawlkit.NewRecordAnchorIdentifier(anchorIdentifier)
	}
	return field
}

func mailDetailExactTimeField(fieldDisplayName string, exactTime time.Time) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentation.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(exactTime)},
				},
			},
		},
	}
}

func formatSender(displayName, emailAddress string) string {
	displayName, emailAddress = strings.TrimSpace(displayName), strings.TrimSpace(emailAddress)
	switch {
	case displayName != "" && emailAddress != "":
		return displayName + " <" + emailAddress + ">"
	case displayName != "":
		return displayName
	default:
		return emailAddress
	}
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
