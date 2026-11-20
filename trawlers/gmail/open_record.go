package gmail

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	email "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/email"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
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
	openedEmailRecord, err := projectOpenedGmailMessageRecord(value)
	if err != nil {
		return nil, err
	}
	record := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedEmailRecord.GetCanonicalEmailMessageRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_OpenedEmailMessageRecord{
			OpenedEmailMessageRecord: openedEmailRecord,
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func projectOpenedGmailMessageRecord(value archive.OpenResult) (*email.OpenedEmailMessageRecord, error) {
	record := &email.OpenedEmailMessageRecord{
		CanonicalEmailMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(value.Ref),
		EmailMessageHeaders: &email.OpenedEmailMessageHeaders{
			RecipientEmailAddresses: value.Headers.ToAddress,
			EmailSubject:            value.Headers.Subject,
		},
		EmailMessageIsUnread:    value.Unread,
		EmailMessageAttachments: make([]*email.OpenedEmailMessageAttachment, 0, len(value.Attachments)),
		EmailMessageBodyText:    value.Body,
		EmailSourceDetails: &email.OpenedEmailMessageRecord_GmailEmailSourceDetails{
			GmailEmailSourceDetails: &email.GmailEmailSourceDetails{
				GmailMessageIdentifier: value.ID,
				GmailThreadIdentifier:  value.ThreadID,
				GmailLabelNames:        append([]string(nil), value.LabelNames...),
			},
		},
	}
	if !value.DeletedFromSourceTime.IsZero() {
		record.DeletedFromEmailSourceTime = timestamppb.New(value.DeletedFromSourceTime)
	}
	if messageTimeText := strings.TrimSpace(value.Time); messageTimeText != "" {
		messageTime, err := time.Parse(time.RFC3339Nano, messageTimeText)
		if err != nil {
			return nil, fmt.Errorf("invalid Gmail internal message time with RFC822 Date header fallback %q: %w", messageTimeText, err)
		}
		record.EmailMessageTime = timestamppb.New(messageTime)
	}
	setOptionalString(&record.EmailMessageHeaders.SenderDisplayName, value.Headers.FromName)
	setOptionalString(&record.EmailMessageHeaders.SenderEmailAddress, value.Headers.FromAddress)
	setOptionalString(&record.EmailMessageHeaders.CopiedRecipientEmailAddresses, value.Headers.CcAddress)
	setOptionalString(&record.EmailMessageHeaders.ReplyToEmailAddresses, value.Headers.ReplyToAddress)
	setOptionalString(&record.EmailMessageHeaders.MailingListIdentifier, value.Headers.MailingListIdentifier)
	setOptionalString(&record.EmailMessageHeaders.Rfc5322MessageIdentifier, value.Headers.RFC5322MessageIdentifier)
	setOptionalString(&record.EmailMessageHeaders.InReplyToMessageIdentifiers, value.Headers.InReplyToMessageIdentifiers)
	for _, attachment := range value.Attachments {
		byteCount := attachment.Size
		record.EmailMessageAttachments = append(record.EmailMessageAttachments, &email.OpenedEmailMessageAttachment{
			AttachmentFilename:  attachment.Filename,
			AttachmentMediaType: attachment.MIMEType,
			AttachmentByteCount: &byteCount,
		})
	}
	return record, nil
}

func setOptionalString(target **string, value string) {
	if value != "" {
		*target = &value
	}
}
