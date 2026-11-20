package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/mail/internal/applemailsource"
	"github.com/opentrawl/opentrawl/trawlers/mail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const parsedMessagesPerArchiveTransaction = 500

type mailUpdateCounts struct {
	messagesParsedFromAppleMailFiles    int
	messagesParsedFromStoredOriginals   int
	messagesWithMailboxOrReadChange     int
	messagesWithoutLocalAppleMailFile   int
	messagesWithUnreadableAppleMailFile int
	messagesDeletedFromAppleMail        int
	messagesAdded                       int
}

// Update reads only Apple Mail message files that are new or changed since
// the last update. Messages that Apple Mail no longer has stay in the archive
// and are marked deleted from source.
func (*Crawler) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*updatecontract.TrawlerArchiveUpdateReport, error) {
	updateStartedTime := time.Now()
	homeDirectoryPath, err := os.UserHomeDir()
	if err != nil {
		return nil, appleMailSourceError(err)
	}
	location, err := applemailsource.LocateAppleMailStore(homeDirectoryPath)
	if err != nil {
		return nil, appleMailSourceError(err)
	}
	envelopeIndex, err := applemailsource.ReadAppleMailEnvelopeIndex(ctx, location.EnvelopeIndexDatabasePath)
	if err != nil {
		return nil, appleMailSourceError(err)
	}
	accounts, err := applemailsource.ReadAppleMailAccounts(ctx, location.AccountsDatabasePath)
	if err != nil {
		return nil, appleMailSourceError(err)
	}
	boundGmailOwnerEmailAddress, err := readBoundGmailOwnerEmailAddress(ctx, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, newMailCommandError("gmail_archive_unreadable", "Gmail archive could not be read to avoid duplicate mail", err)
	}
	archiveStore, err := archive.Use(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, newMailCommandError("archive_open_failed", "cannot open the archive database", err)
	}
	liveSourceStates, err := archiveStore.LiveArchivedMailMessageSourceStates(ctx)
	if err != nil {
		return nil, err
	}
	includedAccounts := includedAppleMailAccounts(req, envelopeIndex, accounts, boundGmailOwnerEmailAddress)
	emlxFilesByAccount := map[applemailsource.AppleMailAccountIdentifier]map[applemailsource.AppleMailMessageRowIdentifier]applemailsource.AppleMailEmlxFile{}
	for accountIdentifier := range includedAccounts {
		files, err := applemailsource.FindAppleMailEmlxFiles(filepath.Join(location.AppleMailVersionDirectoryPath, string(accountIdentifier)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, appleMailSourceError(err)
		}
		emlxFilesByAccount[accountIdentifier] = files
	}
	originalStore := mailparse.NewRawEmailOriginalStore(filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath))
	batch := archive.MailArchiveUpdateBatch{DeletedFromSourceTime: updateStartedTime}
	for _, account := range includedAccounts {
		batch.AppleMailAccounts = append(batch.AppleMailAccounts, account)
	}
	counts := mailUpdateCounts{}
	flush := func() error {
		result, err := archiveStore.ApplyMailArchiveUpdateBatch(ctx, batch)
		if err != nil {
			return err
		}
		counts.messagesAdded += result.ArchivedMailMessagesAdded
		batch = archive.MailArchiveUpdateBatch{DeletedFromSourceTime: updateStartedTime}
		return nil
	}
	messagesStillInAppleMail := map[applemailsource.AppleMailMessageRowIdentifier]struct{}{}
	for messageIndex, envelopeMessage := range envelopeIndex.AppleMailMessagesNotDeleted {
		mailbox, found := envelopeIndex.AppleMailMailboxesByRowIdentifier[envelopeMessage.AppleMailMailboxRowIdentifier]
		if !found || mailbox.IsJunkOrTrashMailbox() {
			continue
		}
		if _, included := includedAccounts[mailbox.AppleMailAccountIdentifier]; !included {
			continue
		}
		rowIdentifier := envelopeMessage.AppleMailMessageRowIdentifier
		messagesStillInAppleMail[rowIdentifier] = struct{}{}
		emlxFile, hasFile := emlxFilesByAccount[mailbox.AppleMailAccountIdentifier][rowIdentifier]
		liveSourceState, archived := liveSourceStates[rowIdentifier]
		messageIsUnread := !envelopeMessage.AppleMailMessageIsRead
		if !hasFile {
			if !archived {
				counts.messagesWithoutLocalAppleMailFile++
			} else if liveSourceState.AppleMailMailboxURL != mailbox.AppleMailMailboxURL || liveSourceState.MessageIsUnread != messageIsUnread {
				batch.MailboxOrReadStateChanges = append(batch.MailboxOrReadStateChanges, archive.MailMessageMailboxOrReadStateChange{
					ArchivedMailMessageIdentifier: liveSourceState.ArchivedMailMessageIdentifier,
					AppleMailMailboxURL:           mailbox.AppleMailMailboxURL,
					AppleMailMailboxName:          mailbox.AppleMailMailboxName,
					MessageIsUnread:               messageIsUnread,
				})
				counts.messagesWithMailboxOrReadChange++
			}
			continue
		}
		if archived && liveSourceState.AppleMailEmlxFile == emlxFile && liveSourceState.EmailParserRevision == mailparse.EmailParserRevision {
			if liveSourceState.AppleMailMailboxURL != mailbox.AppleMailMailboxURL || liveSourceState.MessageIsUnread != messageIsUnread {
				batch.MailboxOrReadStateChanges = append(batch.MailboxOrReadStateChanges, archive.MailMessageMailboxOrReadStateChange{
					ArchivedMailMessageIdentifier: liveSourceState.ArchivedMailMessageIdentifier,
					AppleMailMailboxURL:           mailbox.AppleMailMailboxURL,
					AppleMailMailboxName:          mailbox.AppleMailMailboxName,
					MessageIsUnread:               messageIsUnread,
				})
				counts.messagesWithMailboxOrReadChange++
			}
			continue
		}
		var rawEmailMessage []byte
		var originalSHA256 mailparse.RawEmailOriginalSHA256
		if archived && liveSourceState.AppleMailEmlxFile == emlxFile {
			originalSHA256 = liveSourceState.RawEmailOriginalSHA256
			rawEmailMessage, err = originalStore.ReadRawEmailOriginal(originalSHA256)
			if err != nil {
				return nil, err
			}
			counts.messagesParsedFromStoredOriginals++
		} else {
			rawEmailMessage, err = applemailsource.ReadRawEmailMessageFromEmlxFile(emlxFile.EmlxFilePath)
			if err != nil {
				// Mail.app may move or rewrite a file between the folder walk
				// and this read; the next update reads it again.
				counts.messagesWithUnreadableAppleMailFile++
				logMailUpdateEvent(req, "message_file_unreadable", "row="+strconv.FormatInt(int64(rowIdentifier), 10)+" error="+strconv.Quote(err.Error()))
				continue
			}
			if originalSHA256, err = originalStore.StoreRawEmailOriginalOnce(rawEmailMessage); err != nil {
				return nil, err
			}
			counts.messagesParsedFromAppleMailFiles++
		}
		batch.ArchivedMailMessagesToWrite = append(batch.ArchivedMailMessagesToWrite,
			newArchivedMailMessage(mailbox, envelopeMessage, emlxFile, originalSHA256, rawEmailMessage))
		if len(batch.ArchivedMailMessagesToWrite) >= parsedMessagesPerArchiveTransaction {
			if err := flush(); err != nil {
				return nil, err
			}
			reportMailUpdateProgress(req, messageIndex+1, len(envelopeIndex.AppleMailMessagesNotDeleted))
		}
	}
	for rowIdentifier, liveSourceState := range liveSourceStates {
		if _, included := includedAccounts[liveSourceState.AppleMailAccountIdentifier]; !included {
			continue
		}
		if _, stillInAppleMail := messagesStillInAppleMail[rowIdentifier]; !stillInAppleMail {
			batch.ArchivedMailMessagesDeletedFromSource = append(batch.ArchivedMailMessagesDeletedFromSource, liveSourceState.ArchivedMailMessageIdentifier)
		}
	}
	counts.messagesDeletedFromAppleMail = len(batch.ArchivedMailMessagesDeletedFromSource)
	messagesDeletedWithOlderParse, err := archiveStore.MailMessagesDeletedFromSourceWithOlderParse(ctx, mailparse.EmailParserRevision)
	if err != nil {
		return nil, err
	}
	for _, message := range messagesDeletedWithOlderParse {
		rawEmailMessage, err := originalStore.ReadRawEmailOriginal(message.RawEmailOriginalSHA256)
		if err != nil {
			return nil, err
		}
		parsed := mailparse.ParseRawEmailMessage(rawEmailMessage)
		batch.ReparsedMailMessagesDeletedFromSource = append(batch.ReparsedMailMessagesDeletedFromSource, archive.ReparsedMailMessageDeletedFromSource{
			ArchivedMailMessageIdentifier: message.ArchivedMailMessageIdentifier,
			EmailParserRevision:           mailparse.EmailParserRevision,
			ParsedEmailMessage:            parsed,
			ArchivedMailAttachments:       archivedAttachmentsWithoutAppleMailFolder(parsed, message.EmlxFileIsPartial),
		})
		counts.messagesParsedFromStoredOriginals++
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := archiveStore.MarkUpdateCompleted(ctx, time.Now()); err != nil {
		return nil, err
	}
	reportMailUpdateProgress(req, len(envelopeIndex.AppleMailMessagesNotDeleted), len(envelopeIndex.AppleMailMessagesNotDeleted))
	logMailUpdateEvent(req, "update_complete", strings.Join([]string{
		"parsed_from_apple_mail_files=" + strconv.Itoa(counts.messagesParsedFromAppleMailFiles),
		"parsed_from_stored_originals=" + strconv.Itoa(counts.messagesParsedFromStoredOriginals),
		"mailbox_or_read_changes=" + strconv.Itoa(counts.messagesWithMailboxOrReadChange),
		"without_local_file=" + strconv.Itoa(counts.messagesWithoutLocalAppleMailFile),
		"unreadable_file=" + strconv.Itoa(counts.messagesWithUnreadableAppleMailFile),
		"deleted_from_apple_mail=" + strconv.Itoa(counts.messagesDeletedFromAppleMail),
		"added=" + strconv.Itoa(counts.messagesAdded),
		"elapsed_ms=" + strconv.FormatInt(time.Since(updateStartedTime).Milliseconds(), 10),
	}, " "))
	return &updatecontract.TrawlerArchiveUpdateReport{ArchiveRecordCountAddedByThisUpdate: proto.Uint64(uint64(counts.messagesAdded))}, nil
}

// includedAppleMailAccounts returns every account with a mailbox, except a
// Gmail account whose address the Gmail trawler is bound to. iCloud's user
// name can be a Gmail address too, so an account must be a Gmail account
// before its address is compared.
func includedAppleMailAccounts(
	req *trawlkit.TrawlerCommandExecutionRequest,
	envelopeIndex applemailsource.AppleMailEnvelopeIndexContents,
	accounts map[applemailsource.AppleMailAccountIdentifier]applemailsource.AppleMailAccount,
	boundGmailOwnerEmailAddress mailparse.NormalizedEmailAddress,
) map[applemailsource.AppleMailAccountIdentifier]applemailsource.AppleMailAccount {
	mailboxesByAccount := map[applemailsource.AppleMailAccountIdentifier][]applemailsource.AppleMailMailbox{}
	for _, mailbox := range envelopeIndex.AppleMailMailboxesByRowIdentifier {
		if mailbox.AppleMailAccountIdentifier != "" {
			mailboxesByAccount[mailbox.AppleMailAccountIdentifier] = append(mailboxesByAccount[mailbox.AppleMailAccountIdentifier], mailbox)
		}
	}
	included := map[applemailsource.AppleMailAccountIdentifier]applemailsource.AppleMailAccount{}
	for accountIdentifier, mailboxes := range mailboxesByAccount {
		account, found := accounts[accountIdentifier]
		if !found {
			account = applemailsource.AppleMailAccount{AppleMailAccountIdentifier: accountIdentifier}
		}
		if boundGmailOwnerEmailAddress != "" && account.IsGmailAccount(mailboxes) && account.NormalizedUserEmailAddress() == boundGmailOwnerEmailAddress {
			logMailUpdateEvent(req, "gmail_account_skipped", "account="+string(accountIdentifier)+" reason=archived_by_gmail_trawler")
			continue
		}
		included[accountIdentifier] = account
	}
	return included
}

func newArchivedMailMessage(
	mailbox applemailsource.AppleMailMailbox,
	envelopeMessage applemailsource.AppleMailEnvelopeMessage,
	emlxFile applemailsource.AppleMailEmlxFile,
	originalSHA256 mailparse.RawEmailOriginalSHA256,
	rawEmailMessage []byte,
) archive.ArchivedMailMessage {
	parsed := mailparse.ParseRawEmailMessage(rawEmailMessage)
	attachments := make([]archive.ArchivedMailAttachment, 0, len(parsed.EmailAttachments))
	for _, attachment := range parsed.EmailAttachments {
		archived := archive.ArchivedMailAttachment{AttachmentFilename: attachment.AttachmentFilename, AttachmentMediaType: attachment.AttachmentMediaType}
		byteCount := attachment.AttachmentDecodedByteCount
		if emlxFile.EmlxFileIsPartial && byteCount == 0 {
			if detachedByteCount, found := applemailsource.DetachedAttachmentByteCount(emlxFile.EmlxFilePath, envelopeMessage.AppleMailMessageRowIdentifier, attachment.AttachmentMIMESectionPath); found {
				archived.AttachmentByteCount = &detachedByteCount
			}
		} else {
			archived.AttachmentByteCount = &byteCount
		}
		attachments = append(attachments, archived)
	}
	receivedTime := envelopeMessage.AppleMailMessageReceivedTime
	if receivedTime.IsZero() {
		receivedTime = parsed.DateHeaderTime
	}
	return archive.ArchivedMailMessage{
		AppleMailAccountIdentifier:    mailbox.AppleMailAccountIdentifier,
		AppleMailMailboxURL:           mailbox.AppleMailMailboxURL,
		AppleMailMailboxName:          mailbox.AppleMailMailboxName,
		AppleMailMessageRowIdentifier: envelopeMessage.AppleMailMessageRowIdentifier,
		AppleMailEmlxFile:             emlxFile,
		RawEmailOriginalSHA256:        originalSHA256,
		EmailParserRevision:           mailparse.EmailParserRevision,
		ParsedEmailMessage:            parsed,
		ArchivedMailAttachments:       attachments,
		MessageReceivedTime:           receivedTime,
		MessageIsUnread:               !envelopeMessage.AppleMailMessageIsRead,
	}
}

// archivedAttachmentsWithoutAppleMailFolder sizes attachments from the raw
// original alone. A partial original holds no attachment bodies, so their
// sizes stay unknown.
func archivedAttachmentsWithoutAppleMailFolder(parsed mailparse.ParsedEmailMessage, originalIsPartial bool) []archive.ArchivedMailAttachment {
	attachments := make([]archive.ArchivedMailAttachment, 0, len(parsed.EmailAttachments))
	for _, attachment := range parsed.EmailAttachments {
		archived := archive.ArchivedMailAttachment{AttachmentFilename: attachment.AttachmentFilename, AttachmentMediaType: attachment.AttachmentMediaType}
		if byteCount := attachment.AttachmentDecodedByteCount; !originalIsPartial || byteCount > 0 {
			archived.AttachmentByteCount = &byteCount
		}
		attachments = append(attachments, archived)
	}
	return attachments
}

// readBoundGmailOwnerEmailAddress reads the Google account the Gmail trawler
// is bound to from its archive beside this one, without writing to it.
func readBoundGmailOwnerEmailAddress(ctx context.Context, mailArchivePath string) (mailparse.NormalizedEmailAddress, error) {
	gmailArchivePath := filepath.Join(filepath.Dir(filepath.Dir(mailArchivePath)), "gmail", "gmail.db")
	if _, err := os.Stat(gmailArchivePath); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	gmailArchive, err := store.OpenReadOnlyWithoutWritingFiles(ctx, gmailArchivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = gmailArchive.Close() }()
	var ownerEmailAddress string
	err = gmailArchive.DB().QueryRowContext(ctx, `
select value from update_state
where source_name = 'gmail' and entity_type = 'owner' and entity_id = 'account_email'`).Scan(&ownerEmailAddress)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Gmail owner address: %w", err)
	}
	return mailparse.NormalizeEmailAddress(ownerEmailAddress), nil
}

func reportMailUpdateProgress(req *trawlkit.TrawlerCommandExecutionRequest, messagesDone, messagesTotal int) {
	if req.ReportTrawlerCommandProgress != nil {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "update", Done: int64(messagesDone), Total: int64(messagesTotal), Message: "reading Apple Mail messages"})
	}
}

func logMailUpdateEvent(req *trawlkit.TrawlerCommandExecutionRequest, event, message string) {
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info(event, message)
	}
}
