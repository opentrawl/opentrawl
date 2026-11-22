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
	location, envelopeIndex, accounts, err := loadAppleMailSources(ctx)
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
	emlxFilesByAccount, err := loadAppleMailEmlxFilesByAccount(location.AppleMailVersionDirectoryPath, includedAccounts)
	if err != nil {
		return nil, appleMailSourceError(err)
	}
	updater := &mailArchiveUpdater{
		req:                req,
		archiveStore:       archiveStore,
		originalStore:      mailparse.NewRawEmailOriginalStore(filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath)),
		updateStartedTime:  updateStartedTime,
		includedAccounts:   includedAccounts,
		emlxFilesByAccount: emlxFilesByAccount,
		liveSourceStates:   liveSourceStates,
		batch:              newMailUpdateBatch(updateStartedTime, includedAccounts),
		stillInAppleMail:   map[applemailsource.AppleMailMessageRowIdentifier]struct{}{},
	}
	if err := updater.processEnvelopeMessages(ctx, envelopeIndex); err != nil {
		return nil, err
	}
	updater.collectDeletedMessages()
	if err := updater.reparseDeletedMessagesWithOlderParse(ctx); err != nil {
		return nil, err
	}
	if err := updater.flush(ctx); err != nil {
		return nil, err
	}
	if err := archiveStore.MarkUpdateCompleted(ctx, time.Now()); err != nil {
		return nil, err
	}
	reportMailUpdateProgress(req, len(envelopeIndex.AppleMailMessagesNotDeleted), len(envelopeIndex.AppleMailMessagesNotDeleted))
	logMailUpdateComplete(req, updater.counts, updateStartedTime)
	return &updatecontract.TrawlerArchiveUpdateReport{ArchiveRecordCountAddedByThisUpdate: proto.Uint64(uint64(updater.counts.messagesAdded))}, nil
}

type mailArchiveUpdater struct {
	req                *trawlkit.TrawlerCommandExecutionRequest
	archiveStore       *archive.Store
	originalStore      mailparse.RawEmailOriginalStore
	updateStartedTime  time.Time
	includedAccounts   map[applemailsource.AppleMailAccountIdentifier]applemailsource.AppleMailAccount
	emlxFilesByAccount map[applemailsource.AppleMailAccountIdentifier]map[applemailsource.AppleMailMessageRowIdentifier]applemailsource.AppleMailEmlxFile
	liveSourceStates   map[applemailsource.AppleMailMessageRowIdentifier]archive.LiveArchivedMailMessageSourceState
	batch              archive.MailArchiveUpdateBatch
	stillInAppleMail   map[applemailsource.AppleMailMessageRowIdentifier]struct{}
	counts             mailUpdateCounts
}

func loadAppleMailSources(ctx context.Context) (
	applemailsource.AppleMailStoreLocation,
	applemailsource.AppleMailEnvelopeIndexContents,
	map[applemailsource.AppleMailAccountIdentifier]applemailsource.AppleMailAccount,
	error,
) {
	homeDirectoryPath, err := os.UserHomeDir()
	if err != nil {
		return applemailsource.AppleMailStoreLocation{}, applemailsource.AppleMailEnvelopeIndexContents{}, nil, err
	}
	location, err := applemailsource.LocateAppleMailStore(homeDirectoryPath)
	if err != nil {
		return applemailsource.AppleMailStoreLocation{}, applemailsource.AppleMailEnvelopeIndexContents{}, nil, err
	}
	envelopeIndex, err := applemailsource.ReadAppleMailEnvelopeIndex(ctx, location.EnvelopeIndexDatabasePath)
	if err != nil {
		return applemailsource.AppleMailStoreLocation{}, applemailsource.AppleMailEnvelopeIndexContents{}, nil, err
	}
	accounts, err := applemailsource.ReadAppleMailAccounts(ctx, location.AccountsDatabasePath)
	if err != nil {
		return applemailsource.AppleMailStoreLocation{}, applemailsource.AppleMailEnvelopeIndexContents{}, nil, err
	}
	return location, envelopeIndex, accounts, nil
}

func loadAppleMailEmlxFilesByAccount(
	versionDir string,
	includedAccounts map[applemailsource.AppleMailAccountIdentifier]applemailsource.AppleMailAccount,
) (map[applemailsource.AppleMailAccountIdentifier]map[applemailsource.AppleMailMessageRowIdentifier]applemailsource.AppleMailEmlxFile, error) {
	emlxFilesByAccount := map[applemailsource.AppleMailAccountIdentifier]map[applemailsource.AppleMailMessageRowIdentifier]applemailsource.AppleMailEmlxFile{}
	for accountIdentifier := range includedAccounts {
		files, err := applemailsource.FindAppleMailEmlxFiles(filepath.Join(versionDir, string(accountIdentifier)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		emlxFilesByAccount[accountIdentifier] = files
	}
	return emlxFilesByAccount, nil
}

func newMailUpdateBatch(
	updateStartedTime time.Time,
	includedAccounts map[applemailsource.AppleMailAccountIdentifier]applemailsource.AppleMailAccount,
) archive.MailArchiveUpdateBatch {
	batch := archive.MailArchiveUpdateBatch{DeletedFromSourceTime: updateStartedTime}
	for _, account := range includedAccounts {
		batch.AppleMailAccounts = append(batch.AppleMailAccounts, account)
	}
	return batch
}

func (u *mailArchiveUpdater) flush(ctx context.Context) error {
	result, err := u.archiveStore.ApplyMailArchiveUpdateBatch(ctx, u.batch)
	if err != nil {
		return err
	}
	u.counts.messagesAdded += result.ArchivedMailMessagesAdded
	u.batch = archive.MailArchiveUpdateBatch{DeletedFromSourceTime: u.updateStartedTime}
	return nil
}

func (u *mailArchiveUpdater) processEnvelopeMessages(ctx context.Context, envelopeIndex applemailsource.AppleMailEnvelopeIndexContents) error {
	for messageIndex, envelopeMessage := range envelopeIndex.AppleMailMessagesNotDeleted {
		if err := u.processOneEnvelopeMessage(ctx, envelopeIndex, envelopeMessage); err != nil {
			return err
		}
		if len(u.batch.ArchivedMailMessagesToWrite) >= parsedMessagesPerArchiveTransaction {
			if err := u.flush(ctx); err != nil {
				return err
			}
			reportMailUpdateProgress(u.req, messageIndex+1, len(envelopeIndex.AppleMailMessagesNotDeleted))
		}
	}
	return nil
}

func (u *mailArchiveUpdater) processOneEnvelopeMessage(
	ctx context.Context,
	envelopeIndex applemailsource.AppleMailEnvelopeIndexContents,
	envelopeMessage applemailsource.AppleMailEnvelopeMessage,
) error {
	mailbox, found := envelopeIndex.AppleMailMailboxesByRowIdentifier[envelopeMessage.AppleMailMailboxRowIdentifier]
	if !found || mailbox.IsJunkOrTrashMailbox() {
		return nil
	}
	if _, included := u.includedAccounts[mailbox.AppleMailAccountIdentifier]; !included {
		return nil
	}
	rowIdentifier := envelopeMessage.AppleMailMessageRowIdentifier
	u.stillInAppleMail[rowIdentifier] = struct{}{}
	emlxFile, hasFile := u.emlxFilesByAccount[mailbox.AppleMailAccountIdentifier][rowIdentifier]
	liveSourceState, archived := u.liveSourceStates[rowIdentifier]
	messageIsUnread := !envelopeMessage.AppleMailMessageIsRead
	if !hasFile {
		if !archived {
			u.counts.messagesWithoutLocalAppleMailFile++
		} else {
			u.recordMailboxOrReadChangeIfNeeded(liveSourceState, mailbox, messageIsUnread)
		}
		return nil
	}
	if archived && liveSourceState.AppleMailEmlxFile == emlxFile && liveSourceState.EmailParserRevision == mailparse.EmailParserRevision {
		u.recordMailboxOrReadChangeIfNeeded(liveSourceState, mailbox, messageIsUnread)
		return nil
	}
	rawEmailMessage, originalSHA256, ok, err := u.loadRawEmailForUpdate(rowIdentifier, emlxFile, liveSourceState, archived)
	if err != nil || !ok {
		return err
	}
	u.batch.ArchivedMailMessagesToWrite = append(u.batch.ArchivedMailMessagesToWrite,
		newArchivedMailMessage(mailbox, envelopeMessage, emlxFile, originalSHA256, rawEmailMessage))
	return nil
}

func (u *mailArchiveUpdater) recordMailboxOrReadChangeIfNeeded(
	liveSourceState archive.LiveArchivedMailMessageSourceState,
	mailbox applemailsource.AppleMailMailbox,
	messageIsUnread bool,
) {
	if liveSourceState.AppleMailMailboxURL == mailbox.AppleMailMailboxURL && liveSourceState.MessageIsUnread == messageIsUnread {
		return
	}
	u.batch.MailboxOrReadStateChanges = append(u.batch.MailboxOrReadStateChanges, archive.MailMessageMailboxOrReadStateChange{
		ArchivedMailMessageIdentifier: liveSourceState.ArchivedMailMessageIdentifier,
		AppleMailMailboxURL:           mailbox.AppleMailMailboxURL,
		AppleMailMailboxName:          mailbox.AppleMailMailboxName,
		MessageIsUnread:               messageIsUnread,
	})
	u.counts.messagesWithMailboxOrReadChange++
}

func (u *mailArchiveUpdater) loadRawEmailForUpdate(
	rowIdentifier applemailsource.AppleMailMessageRowIdentifier,
	emlxFile applemailsource.AppleMailEmlxFile,
	liveSourceState archive.LiveArchivedMailMessageSourceState,
	archived bool,
) ([]byte, mailparse.RawEmailOriginalSHA256, bool, error) {
	if archived && liveSourceState.AppleMailEmlxFile == emlxFile {
		originalSHA256 := liveSourceState.RawEmailOriginalSHA256
		rawEmailMessage, err := u.originalStore.ReadRawEmailOriginal(originalSHA256)
		if err != nil {
			return nil, "", false, err
		}
		u.counts.messagesParsedFromStoredOriginals++
		return rawEmailMessage, originalSHA256, true, nil
	}
	rawEmailMessage, err := applemailsource.ReadRawEmailMessageFromEmlxFile(emlxFile.EmlxFilePath)
	if err != nil {
		// Mail.app may move or rewrite a file between the folder walk
		// and this read; the next update reads it again.
		u.counts.messagesWithUnreadableAppleMailFile++
		logMailUpdateEvent(u.req, "message_file_unreadable", "row="+strconv.FormatInt(int64(rowIdentifier), 10)+" error="+strconv.Quote(err.Error()))
		return nil, "", false, nil
	}
	originalSHA256, err := u.originalStore.StoreRawEmailOriginalOnce(rawEmailMessage)
	if err != nil {
		return nil, "", false, err
	}
	u.counts.messagesParsedFromAppleMailFiles++
	return rawEmailMessage, originalSHA256, true, nil
}

func (u *mailArchiveUpdater) collectDeletedMessages() {
	for rowIdentifier, liveSourceState := range u.liveSourceStates {
		if _, included := u.includedAccounts[liveSourceState.AppleMailAccountIdentifier]; !included {
			continue
		}
		if _, stillInAppleMail := u.stillInAppleMail[rowIdentifier]; !stillInAppleMail {
			u.batch.ArchivedMailMessagesDeletedFromSource = append(u.batch.ArchivedMailMessagesDeletedFromSource, liveSourceState.ArchivedMailMessageIdentifier)
		}
	}
	u.counts.messagesDeletedFromAppleMail = len(u.batch.ArchivedMailMessagesDeletedFromSource)
}

func (u *mailArchiveUpdater) reparseDeletedMessagesWithOlderParse(ctx context.Context) error {
	messagesDeletedWithOlderParse, err := u.archiveStore.MailMessagesDeletedFromSourceWithOlderParse(ctx, mailparse.EmailParserRevision)
	if err != nil {
		return err
	}
	for _, message := range messagesDeletedWithOlderParse {
		rawEmailMessage, err := u.originalStore.ReadRawEmailOriginal(message.RawEmailOriginalSHA256)
		if err != nil {
			return err
		}
		parsed := mailparse.ParseRawEmailMessage(rawEmailMessage)
		u.batch.ReparsedMailMessagesDeletedFromSource = append(u.batch.ReparsedMailMessagesDeletedFromSource, archive.ReparsedMailMessageDeletedFromSource{
			ArchivedMailMessageIdentifier: message.ArchivedMailMessageIdentifier,
			EmailParserRevision:           mailparse.EmailParserRevision,
			ParsedEmailMessage:            parsed,
			ArchivedMailAttachments:       archivedAttachmentsWithoutAppleMailFolder(parsed, message.EmlxFileIsPartial),
		})
		u.counts.messagesParsedFromStoredOriginals++
	}
	return nil
}

func logMailUpdateComplete(req *trawlkit.TrawlerCommandExecutionRequest, counts mailUpdateCounts, updateStartedTime time.Time) {
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
// is bound to from its archive beside this one. An absent or uninitialised
// Gmail archive means no Google account is bound yet.
func readBoundGmailOwnerEmailAddress(ctx context.Context, mailArchivePath string) (mailparse.NormalizedEmailAddress, error) {
	gmailArchivePath := filepath.Join(filepath.Dir(filepath.Dir(mailArchivePath)), "gmail", "gmail.db")
	if _, err := os.Stat(gmailArchivePath); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	gmailArchive, err := store.OpenReadOnly(ctx, gmailArchivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = gmailArchive.Close() }()
	var hasUpdateStateTable int
	err = gmailArchive.DB().QueryRowContext(ctx, `
select 1 from sqlite_master where type = 'table' and name = 'update_state'`).Scan(&hasUpdateStateTable)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("check Gmail update_state table: %w", err)
	}
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
