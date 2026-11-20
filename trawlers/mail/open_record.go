package mail

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/mail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	email "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/email"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
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
		return nil, render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage)
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
		CanonicalRecordReference: openedRecord.GetCanonicalEmailMessageRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_OpenedEmailMessageRecord{
			OpenedEmailMessageRecord: openedRecord,
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func openedAppleMailMessageRecord(opened archive.OpenedMailMessage) *email.OpenedEmailMessageRecord {
	record := &email.OpenedEmailMessageRecord{
		CanonicalEmailMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(opened.MailMessageRecordReference),
		EmailMessageHeaders: &email.OpenedEmailMessageHeaders{
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
		EmailMessageIsUnread: opened.MessageIsUnread,
		EmailMessageBodyText: opened.EmailBodyText,
		EmailSourceDetails: &email.OpenedEmailMessageRecord_AppleMailEmailSourceDetails{
			AppleMailEmailSourceDetails: &email.AppleMailEmailSourceDetails{
				AppleMailAccountDisplayName: opened.AppleMailAccountDisplayName,
				AppleMailMailboxName:        opened.AppleMailMailboxName,
			},
		},
	}
	if !opened.MessageReceivedTime.IsZero() {
		record.EmailMessageTime = timestamppb.New(opened.MessageReceivedTime)
	}
	if !opened.DeletedFromAppleMailTime.IsZero() {
		record.DeletedFromEmailSourceTime = timestamppb.New(opened.DeletedFromAppleMailTime)
	}
	for _, attachment := range opened.ArchivedMailAttachments {
		record.EmailMessageAttachments = append(record.EmailMessageAttachments, &email.OpenedEmailMessageAttachment{
			AttachmentFilename:  attachment.AttachmentFilename,
			AttachmentMediaType: attachment.AttachmentMediaType,
			AttachmentByteCount: attachment.AttachmentByteCount,
		})
	}
	return record
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
