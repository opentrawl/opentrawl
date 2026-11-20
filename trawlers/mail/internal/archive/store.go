// Package archive owns the Mail trawler's SQLite archive, mail.db.
package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/mail/internal/applemailsource"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	MailMessageRecordReferencePrefix = "mail:msg/"
	sourceName                       = "mail"
	updateEntityType                 = "update"
	lastCompletedEntityID            = "last_completed_at"
)

type Store struct {
	store *ckstore.Store
	path  string
}

type ArchivedMailMessageIdentifier int64

type ArchivedMailAttachment struct {
	AttachmentFilename  string
	AttachmentMediaType string
	// AttachmentByteCount is absent when a partial message file holds no
	// attachment body and no detached attachment file was found.
	AttachmentByteCount *int64
}

// ArchivedMailMessage is one parsed Apple Mail message ready to write.
type ArchivedMailMessage struct {
	AppleMailAccountIdentifier    applemailsource.AppleMailAccountIdentifier
	AppleMailMailboxURL           applemailsource.AppleMailMailboxURL
	AppleMailMailboxName          string
	AppleMailMessageRowIdentifier applemailsource.AppleMailMessageRowIdentifier
	AppleMailEmlxFile             applemailsource.AppleMailEmlxFile
	RawEmailOriginalSHA256        mailparse.RawEmailOriginalSHA256
	EmailParserRevision           string
	ParsedEmailMessage            mailparse.ParsedEmailMessage
	ArchivedMailAttachments       []ArchivedMailAttachment
	MessageReceivedTime           time.Time
	MessageIsUnread               bool
}

// LiveArchivedMailMessageSourceState is what the archive last saw of one
// message that Apple Mail still had.
type LiveArchivedMailMessageSourceState struct {
	ArchivedMailMessageIdentifier ArchivedMailMessageIdentifier
	AppleMailMailboxURL           applemailsource.AppleMailMailboxURL
	AppleMailMailboxName          string
	AppleMailEmlxFile             applemailsource.AppleMailEmlxFile
	RawEmailOriginalSHA256        mailparse.RawEmailOriginalSHA256
	EmailParserRevision           string
	MessageIsUnread               bool
}

// MailMessageMailboxOrReadStateChange records a move or a read-state change
// that needs no new parse of the message file.
type MailMessageMailboxOrReadStateChange struct {
	ArchivedMailMessageIdentifier ArchivedMailMessageIdentifier
	AppleMailMailboxURL           applemailsource.AppleMailMailboxURL
	AppleMailMailboxName          string
	MessageIsUnread               bool
}

type MailArchiveUpdateBatch struct {
	AppleMailAccounts                     []applemailsource.AppleMailAccount
	ArchivedMailMessagesToWrite           []ArchivedMailMessage
	MailboxOrReadStateChanges             []MailMessageMailboxOrReadStateChange
	ArchivedMailMessagesDeletedFromSource []ArchivedMailMessageIdentifier
	ReparsedMailMessagesDeletedFromSource []ReparsedMailMessageDeletedFromSource
	DeletedFromSourceTime                 time.Time
}

type MailArchiveUpdateBatchResult struct {
	ArchivedMailMessagesAdded int
}

func Use(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if _, err := st.DB().ExecContext(ctx, schema+state.Schema+shortref.Schema); err != nil {
		return nil, fmt.Errorf("apply archive schema: %w", err)
	}
	return UseExisting(st, path)
}

func UseExisting(st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	return &Store{store: st, path: path}, nil
}

func (s *Store) LiveArchivedMailMessageSourceStates(ctx context.Context) (map[applemailsource.AppleMailMessageRowIdentifier]LiveArchivedMailMessageSourceState, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select id, apple_mail_message_row_identifier, apple_mail_mailbox_url, apple_mail_mailbox_name,
       emlx_file_path, emlx_file_byte_count, emlx_file_modification_time_ns, emlx_file_is_partial,
       raw_email_original_sha256, email_parser_revision, is_unread
from messages
where deleted_from_source_at = ''`)
	if err != nil {
		return nil, fmt.Errorf("read archived message source states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	states := map[applemailsource.AppleMailMessageRowIdentifier]LiveArchivedMailMessageSourceState{}
	for rows.Next() {
		var sourceState LiveArchivedMailMessageSourceState
		var rowIdentifier applemailsource.AppleMailMessageRowIdentifier
		if err := rows.Scan(
			&sourceState.ArchivedMailMessageIdentifier, &rowIdentifier, &sourceState.AppleMailMailboxURL, &sourceState.AppleMailMailboxName,
			&sourceState.AppleMailEmlxFile.EmlxFilePath, &sourceState.AppleMailEmlxFile.EmlxFileByteCount,
			&sourceState.AppleMailEmlxFile.EmlxFileModificationTimeNanoseconds, &sourceState.AppleMailEmlxFile.EmlxFileIsPartial,
			&sourceState.RawEmailOriginalSHA256, &sourceState.EmailParserRevision, &sourceState.MessageIsUnread,
		); err != nil {
			return nil, err
		}
		states[rowIdentifier] = sourceState
	}
	return states, rows.Err()
}

func (s *Store) ApplyMailArchiveUpdateBatch(ctx context.Context, batch MailArchiveUpdateBatch) (MailArchiveUpdateBatchResult, error) {
	var result MailArchiveUpdateBatchResult
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, account := range batch.AppleMailAccounts {
			if _, err := tx.ExecContext(ctx, `
insert into apple_mail_accounts(apple_mail_account_identifier, apple_mail_account_display_name, apple_mail_account_user_name, apple_mail_account_type_identifier)
values (?, ?, ?, ?)
on conflict(apple_mail_account_identifier) do update set
  apple_mail_account_display_name = excluded.apple_mail_account_display_name,
  apple_mail_account_user_name = excluded.apple_mail_account_user_name,
  apple_mail_account_type_identifier = excluded.apple_mail_account_type_identifier`,
				account.AppleMailAccountIdentifier, account.AppleMailAccountDisplayName, account.AppleMailAccountUserName, account.AppleMailAccountTypeIdentifier,
			); err != nil {
				return fmt.Errorf("write Apple Mail account: %w", err)
			}
		}
		writtenIdentifiers := make([]ArchivedMailMessageIdentifier, 0, len(batch.ArchivedMailMessagesToWrite))
		for _, message := range batch.ArchivedMailMessagesToWrite {
			identifier, added, err := writeArchivedMailMessage(ctx, tx, message, batch.DeletedFromSourceTime)
			if err != nil {
				return err
			}
			writtenIdentifiers = append(writtenIdentifiers, identifier)
			if added {
				result.ArchivedMailMessagesAdded++
			}
		}
		for _, change := range batch.MailboxOrReadStateChanges {
			if _, err := tx.ExecContext(ctx, `
update messages set apple_mail_mailbox_url = ?, apple_mail_mailbox_name = ?, is_unread = ? where id = ?`,
				change.AppleMailMailboxURL, change.AppleMailMailboxName, change.MessageIsUnread, change.ArchivedMailMessageIdentifier,
			); err != nil {
				return fmt.Errorf("update message mailbox or read state: %w", err)
			}
		}
		for _, message := range batch.ReparsedMailMessagesDeletedFromSource {
			if err := rewriteParsedColumnsOfMessageDeletedFromSource(ctx, tx, message); err != nil {
				return err
			}
		}
		for _, identifier := range batch.ArchivedMailMessagesDeletedFromSource {
			if err := markArchivedMailMessageDeletedFromSource(ctx, tx, identifier, batch.DeletedFromSourceTime); err != nil {
				return err
			}
		}
		return assignShortReferences(ctx, tx, writtenIdentifiers)
	})
	return result, err
}

// writeArchivedMailMessage keeps the archive identifier, and so every link
// already given out, when a message file changes, such as a partial file
// becoming full. When the same Apple Mail row now holds a different message,
// as after Mail rebuilds its index, the old row is kept as deleted from source.
func writeArchivedMailMessage(ctx context.Context, tx *sql.Tx, message ArchivedMailMessage, now time.Time) (ArchivedMailMessageIdentifier, bool, error) {
	var existingIdentifier ArchivedMailMessageIdentifier
	var existingMessageIdentifier string
	err := tx.QueryRowContext(ctx, `
select id, rfc5322_message_identifier from messages
where apple_mail_message_row_identifier = ? and deleted_from_source_at = ''`, message.AppleMailMessageRowIdentifier,
	).Scan(&existingIdentifier, &existingMessageIdentifier)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("find archived message: %w", err)
	}
	newMessageIdentifier := string(message.ParsedEmailMessage.EmailMessageIdentifier)
	if existingIdentifier != 0 && existingMessageIdentifier != "" && newMessageIdentifier != "" && existingMessageIdentifier != newMessageIdentifier {
		if err := markArchivedMailMessageDeletedFromSource(ctx, tx, existingIdentifier, now); err != nil {
			return 0, false, err
		}
		existingIdentifier = 0
	}
	parsed := message.ParsedEmailMessage
	timeText, timeUnix := "", int64(0)
	if !message.MessageReceivedTime.IsZero() {
		timeText = message.MessageReceivedTime.Local().Format(time.RFC3339)
		timeUnix = message.MessageReceivedTime.Unix()
	}
	columnValues := []any{
		message.AppleMailAccountIdentifier, message.AppleMailMailboxURL, message.AppleMailMailboxName, message.AppleMailMessageRowIdentifier,
		message.AppleMailEmlxFile.EmlxFilePath, message.AppleMailEmlxFile.EmlxFileByteCount,
		message.AppleMailEmlxFile.EmlxFileModificationTimeNanoseconds, message.AppleMailEmlxFile.EmlxFileIsPartial,
		message.RawEmailOriginalSHA256, message.EmailParserRevision,
		newMessageIdentifier,
		mailparse.JoinEmailMessageIdentifiers(parsed.InReplyToEmailMessageIdentifiers),
		mailparse.JoinEmailMessageIdentifiers(parsed.ReferencedEmailMessageIdentifiers),
		mailparse.FormatEmailAddressList(parsed.ReplyToEmailAddresses),
		joinNormalizedEmailAddresses(parsed.DeliveredToEmailAddresses),
		string(parsed.MailingListIdentifier),
		parsed.SenderEmailAddress.DisplayName, string(parsed.SenderEmailAddress.NormalizedEmailAddress),
		mailparse.FormatEmailAddressList(parsed.RecipientEmailAddresses),
		mailparse.FormatEmailAddressList(parsed.CopiedRecipientEmailAddresses),
		parsed.EmailSubject, parsed.EmailBodyText,
		timeText, timeUnix, message.MessageIsUnread,
	}
	added := existingIdentifier == 0
	if added {
		insertResult, err := tx.ExecContext(ctx, `
insert into messages(
  apple_mail_account_identifier, apple_mail_mailbox_url, apple_mail_mailbox_name, apple_mail_message_row_identifier,
  emlx_file_path, emlx_file_byte_count, emlx_file_modification_time_ns, emlx_file_is_partial,
  raw_email_original_sha256, email_parser_revision,
  rfc5322_message_identifier, in_reply_to_message_identifiers, referenced_message_identifiers,
  reply_to_address, delivered_to_address, mailing_list_identifier,
  from_name, from_address, to_address, cc_address, subject, body, time, time_unix, is_unread
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, columnValues...)
		if err != nil {
			return 0, false, fmt.Errorf("insert message: %w", err)
		}
		insertedIdentifier, err := insertResult.LastInsertId()
		if err != nil {
			return 0, false, err
		}
		existingIdentifier = ArchivedMailMessageIdentifier(insertedIdentifier)
	} else {
		if _, err := tx.ExecContext(ctx, `
update messages set
  apple_mail_account_identifier = ?, apple_mail_mailbox_url = ?, apple_mail_mailbox_name = ?, apple_mail_message_row_identifier = ?,
  emlx_file_path = ?, emlx_file_byte_count = ?, emlx_file_modification_time_ns = ?, emlx_file_is_partial = ?,
  raw_email_original_sha256 = ?, email_parser_revision = ?,
  rfc5322_message_identifier = ?, in_reply_to_message_identifiers = ?, referenced_message_identifiers = ?,
  reply_to_address = ?, delivered_to_address = ?, mailing_list_identifier = ?,
  from_name = ?, from_address = ?, to_address = ?, cc_address = ?, subject = ?, body = ?, time = ?, time_unix = ?, is_unread = ?
where id = ?`, append(columnValues, existingIdentifier)...); err != nil {
			return 0, false, fmt.Errorf("update message: %w", err)
		}
	}
	if err := replaceDerivedMessageRows(ctx, tx, existingIdentifier, parsed, message.ArchivedMailAttachments); err != nil {
		return 0, false, err
	}
	return existingIdentifier, added, nil
}

// replaceDerivedMessageRows rewrites the search index, attachment and
// participant rows that are derived from one parsed message.
func replaceDerivedMessageRows(ctx context.Context, tx *sql.Tx, identifier ArchivedMailMessageIdentifier, parsed mailparse.ParsedEmailMessage, attachments []ArchivedMailAttachment) error {
	for _, statement := range []string{
		`delete from messages_fts where id = ?`,
		`delete from attachments where message_id = ?`,
		`delete from message_participants where message_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, identifier); err != nil {
			return fmt.Errorf("clear derived message rows: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `insert into messages_fts(id, subject, body) values (?, ?, ?)`, identifier, parsed.EmailSubject, parsed.EmailBodyText); err != nil {
		return fmt.Errorf("index message: %w", err)
	}
	for _, attachment := range attachments {
		if _, err := tx.ExecContext(ctx, `insert into attachments(message_id, filename, mime_type, size_bytes) values (?, ?, ?, ?)`,
			identifier, attachment.AttachmentFilename, attachment.AttachmentMediaType, attachment.AttachmentByteCount,
		); err != nil {
			return fmt.Errorf("insert attachment: %w", err)
		}
	}
	return insertMessageParticipants(ctx, tx, identifier, parsed)
}

// MailMessageDeletedFromSourceWithOlderParse is a message that left Apple
// Mail before the current parser revision. Its stored original is the only
// copy, so it is parsed again from that original.
type MailMessageDeletedFromSourceWithOlderParse struct {
	ArchivedMailMessageIdentifier ArchivedMailMessageIdentifier
	RawEmailOriginalSHA256        mailparse.RawEmailOriginalSHA256
	EmlxFileIsPartial             bool
}

type ReparsedMailMessageDeletedFromSource struct {
	ArchivedMailMessageIdentifier ArchivedMailMessageIdentifier
	EmailParserRevision           string
	ParsedEmailMessage            mailparse.ParsedEmailMessage
	ArchivedMailAttachments       []ArchivedMailAttachment
}

func (s *Store) MailMessagesDeletedFromSourceWithOlderParse(ctx context.Context, currentEmailParserRevision string) ([]MailMessageDeletedFromSourceWithOlderParse, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select id, raw_email_original_sha256, emlx_file_is_partial from messages
where deleted_from_source_at <> '' and email_parser_revision <> ?`, currentEmailParserRevision)
	if err != nil {
		return nil, fmt.Errorf("read messages deleted from source with an older parse: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var messages []MailMessageDeletedFromSourceWithOlderParse
	for rows.Next() {
		var message MailMessageDeletedFromSourceWithOlderParse
		if err := rows.Scan(&message.ArchivedMailMessageIdentifier, &message.RawEmailOriginalSHA256, &message.EmlxFileIsPartial); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// rewriteParsedColumnsOfMessageDeletedFromSource keeps the row's Apple Mail
// facts as they were when the message left Apple Mail.
func rewriteParsedColumnsOfMessageDeletedFromSource(ctx context.Context, tx *sql.Tx, message ReparsedMailMessageDeletedFromSource) error {
	parsed := message.ParsedEmailMessage
	if _, err := tx.ExecContext(ctx, `
update messages set
  email_parser_revision = ?, rfc5322_message_identifier = ?, in_reply_to_message_identifiers = ?, referenced_message_identifiers = ?,
  reply_to_address = ?, delivered_to_address = ?, mailing_list_identifier = ?,
  from_name = ?, from_address = ?, to_address = ?, cc_address = ?, subject = ?, body = ?
where id = ?`,
		message.EmailParserRevision, string(parsed.EmailMessageIdentifier),
		mailparse.JoinEmailMessageIdentifiers(parsed.InReplyToEmailMessageIdentifiers),
		mailparse.JoinEmailMessageIdentifiers(parsed.ReferencedEmailMessageIdentifiers),
		mailparse.FormatEmailAddressList(parsed.ReplyToEmailAddresses),
		joinNormalizedEmailAddresses(parsed.DeliveredToEmailAddresses),
		string(parsed.MailingListIdentifier),
		parsed.SenderEmailAddress.DisplayName, string(parsed.SenderEmailAddress.NormalizedEmailAddress),
		mailparse.FormatEmailAddressList(parsed.RecipientEmailAddresses),
		mailparse.FormatEmailAddressList(parsed.CopiedRecipientEmailAddresses),
		parsed.EmailSubject, parsed.EmailBodyText,
		message.ArchivedMailMessageIdentifier,
	); err != nil {
		return fmt.Errorf("rewrite parse of message deleted from source: %w", err)
	}
	return replaceDerivedMessageRows(ctx, tx, message.ArchivedMailMessageIdentifier, parsed, message.ArchivedMailAttachments)
}

func markArchivedMailMessageDeletedFromSource(ctx context.Context, tx *sql.Tx, identifier ArchivedMailMessageIdentifier, deletedFromSourceTime time.Time) error {
	if _, err := tx.ExecContext(ctx, `update messages set deleted_from_source_at = ? where id = ? and deleted_from_source_at = ''`,
		deletedFromSourceTime.UTC().Format(time.RFC3339), identifier,
	); err != nil {
		return fmt.Errorf("mark message deleted from source: %w", err)
	}
	return nil
}

func assignShortReferences(ctx context.Context, tx *sql.Tx, identifiers []ArchivedMailMessageIdentifier) error {
	candidates := make([]trawlkit.ShortReferenceAssignmentCandidate, 0, len(identifiers))
	for _, identifier := range identifiers {
		candidates = append(candidates, trawlkit.ShortReferenceAssignmentCandidate{
			StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(MailMessageRecordReference(identifier)),
		})
	}
	return trawlkit.AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(ctx, tx, candidates)
}

func MailMessageRecordReference(identifier ArchivedMailMessageIdentifier) string {
	return MailMessageRecordReferencePrefix + strconv.FormatInt(int64(identifier), 10)
}

func parseMailMessageRecordReference(reference string) (ArchivedMailMessageIdentifier, error) {
	identifierText, found := strings.CutPrefix(strings.TrimSpace(reference), MailMessageRecordReferencePrefix)
	identifier, err := strconv.ParseInt(identifierText, 10, 64)
	if !found || err != nil || identifier <= 0 {
		return 0, fmt.Errorf("reference must look like %s<number>", MailMessageRecordReferencePrefix)
	}
	return ArchivedMailMessageIdentifier(identifier), nil
}

func joinNormalizedEmailAddresses(addresses []mailparse.NormalizedEmailAddress) string {
	texts := make([]string, 0, len(addresses))
	for _, address := range addresses {
		texts = append(texts, string(address))
	}
	return strings.Join(texts, ", ")
}

func (s *Store) MarkUpdateCompleted(ctx context.Context, completedTime time.Time) error {
	return state.New(s.store.DB()).Set(ctx, sourceName, updateEntityType, lastCompletedEntityID, completedTime.UTC().Format(time.RFC3339))
}

func (s *Store) LastSuccessfullyCompletedUpdateTime(ctx context.Context) (time.Time, error) {
	record, found, err := state.New(s.store.DB()).Get(ctx, sourceName, updateEntityType, lastCompletedEntityID)
	if err != nil || !found {
		return time.Time{}, err
	}
	completedTime, err := time.Parse(time.RFC3339, record.Value)
	if err != nil {
		return record.UpdatedAt, nil
	}
	return completedTime, nil
}
