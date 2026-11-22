// Package archive owns the Gmail trawler's SQLite archive, gmail.db.
package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/gmailapi"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	sourceName                 = "gmail"
	updateEntityType           = "update"
	lastStartedEntityID        = "last_started_at"
	lastDoneEntityID           = "last_completed_at"
	mailboxEntityType          = "mailbox"
	gmailHistoryCursorEntityID = "history_cursor"
)

type Store struct {
	store *ckstore.Store
	path  string
}

type UpdateMarkers struct {
	HasCompleted          bool
	PreviousRunIncomplete bool
	LastCompletedAt       time.Time
}

func Use(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if _, err := st.DB().ExecContext(ctx, schema+state.Schema+shortref.Schema); err != nil {
		return nil, fmt.Errorf("apply archive schema: %w", err)
	}
	if err := ensureMessagesFTSIncludesParticipants(ctx, st.DB()); err != nil {
		return nil, fmt.Errorf("ensure message search index schema: %w", err)
	}
	return UseExisting(ctx, st, path)
}

func ensureMessagesFTSIncludesParticipants(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `pragma table_info(messages_fts)`)
	if err != nil {
		return err
	}
	hasParticipants := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "participants" {
			hasParticipants = true
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if hasParticipants {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
drop table if exists messages_fts;
create virtual table messages_fts using fts5(
  id unindexed,
  subject,
  body,
  participants
);
insert into messages_fts(rowid, id, subject, body, participants)
select rowid, id, subject, body,
       trim(from_name || ' ' || from_address || ' ' || to_address || ' ' || cc_address)
from messages;`); err != nil {
		return err
	}
	return nil
}

func UseExisting(_ context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	return &Store{store: st, path: path}, nil
}

// WriteArchivedGmailMessages writes fetched messages without moving the
// history cursor, so a failed update fetches nothing twice and loses nothing.
func (s *Store) WriteArchivedGmailMessages(ctx context.Context, messages []ArchivedGmailMessage) (int, error) {
	added := 0
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		added, err = writeArchivedGmailMessages(ctx, tx, messages)
		return err
	})
	return added, err
}

func writeArchivedGmailMessages(ctx context.Context, tx *sql.Tx, messages []ArchivedGmailMessage) (int, error) {
	added := 0
	writtenIdentifiers := make([]gmailapi.GmailMessageIdentifier, 0, len(messages))
	for _, message := range messages {
		messageWasAdded, err := writeArchivedGmailMessage(ctx, tx, message)
		if err != nil {
			return 0, err
		}
		if messageWasAdded {
			added++
		}
		writtenIdentifiers = append(writtenIdentifiers, message.GmailMessageIdentifier)
	}
	return added, assignShortReferences(ctx, tx, writtenIdentifiers)
}

// writeArchivedGmailMessage inserts a message or rewrites it. A message
// that returns to Gmail, such as one taken out of Trash, is live again.
func writeArchivedGmailMessage(ctx context.Context, tx *sql.Tx, message ArchivedGmailMessage) (bool, error) {
	var existing int
	if err := tx.QueryRowContext(ctx, `select count(*) from messages where id = ?`, message.GmailMessageIdentifier).Scan(&existing); err != nil {
		return false, err
	}
	parsed := message.ParsedEmailMessage
	timeText, timeUnix := "", int64(0)
	if message.GmailInternalDateMilliseconds != 0 {
		messageTime := time.UnixMilli(message.GmailInternalDateMilliseconds)
		timeText, timeUnix = formatArchiveTime(messageTime), messageTime.Unix()
	}
	if _, err := tx.ExecContext(ctx, `
insert into messages(
  id, thread_id, history_id, internal_date_ms, raw_email_original_sha256, email_parser_revision,
  rfc5322_message_identifier, in_reply_to_message_identifiers, referenced_message_identifiers,
  reply_to_address, delivered_to_address, mailing_list_identifier,
  time, time_unix, from_name, from_address, to_address, cc_address, subject, body,
  labels_json, is_unread, deleted_from_source_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')
on conflict(id) do update set
  thread_id = excluded.thread_id,
  history_id = excluded.history_id,
  internal_date_ms = excluded.internal_date_ms,
  raw_email_original_sha256 = excluded.raw_email_original_sha256,
  email_parser_revision = excluded.email_parser_revision,
  rfc5322_message_identifier = excluded.rfc5322_message_identifier,
  in_reply_to_message_identifiers = excluded.in_reply_to_message_identifiers,
  referenced_message_identifiers = excluded.referenced_message_identifiers,
  reply_to_address = excluded.reply_to_address,
  delivered_to_address = excluded.delivered_to_address,
  mailing_list_identifier = excluded.mailing_list_identifier,
  time = excluded.time,
  time_unix = excluded.time_unix,
  from_name = excluded.from_name,
  from_address = excluded.from_address,
  to_address = excluded.to_address,
  cc_address = excluded.cc_address,
  subject = excluded.subject,
  body = excluded.body,
  labels_json = excluded.labels_json,
  is_unread = excluded.is_unread,
  deleted_from_source_at = ''`,
		message.GmailMessageIdentifier, message.GmailThreadIdentifier, message.GmailHistoryIdentifier, message.GmailInternalDateMilliseconds,
		message.RawEmailOriginalSHA256, message.EmailParserRevision,
		string(parsed.EmailMessageIdentifier),
		mailparse.JoinEmailMessageIdentifiers(parsed.InReplyToEmailMessageIdentifiers),
		mailparse.JoinEmailMessageIdentifiers(parsed.ReferencedEmailMessageIdentifiers),
		mailparse.FormatEmailAddressList(parsed.ReplyToEmailAddresses),
		joinNormalizedEmailAddresses(parsed.DeliveredToEmailAddresses),
		string(parsed.MailingListIdentifier),
		timeText, timeUnix,
		parsed.SenderEmailAddress.DisplayName, string(parsed.SenderEmailAddress.NormalizedEmailAddress),
		mailparse.FormatEmailAddressList(parsed.RecipientEmailAddresses),
		mailparse.FormatEmailAddressList(parsed.CopiedRecipientEmailAddresses),
		parsed.EmailSubject, parsed.EmailBodyText,
		labelsJSON(message.GmailLabelIdentifiers), gmailLabelsMarkMessageUnread(message.GmailLabelIdentifiers),
	); err != nil {
		return false, fmt.Errorf("write message: %w", err)
	}
	if err := replaceDerivedMessageRows(ctx, tx, message.GmailMessageIdentifier, existing == 0, parsed); err != nil {
		return false, err
	}
	return existing == 0, nil
}

// replaceDerivedMessageRows rewrites the search index, attachment and
// participant rows derived from one parsed message.
func replaceDerivedMessageRows(ctx context.Context, tx *sql.Tx, identifier gmailapi.GmailMessageIdentifier, messageIsNew bool, parsed mailparse.ParsedEmailMessage) error {
	var messageRowID int64
	if err := tx.QueryRowContext(ctx, `select rowid from messages where id = ?`, identifier).Scan(&messageRowID); err != nil {
		return fmt.Errorf("lookup message rowid: %w", err)
	}
	if !messageIsNew {
		if _, err := tx.ExecContext(ctx, `delete from messages_fts where rowid = ?`, messageRowID); err != nil {
			return fmt.Errorf("clear message search index row: %w", err)
		}
		for _, statement := range []string{
			`delete from attachments where message_id = ?`,
			`delete from message_participants where message_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, identifier); err != nil {
				return fmt.Errorf("clear derived message rows: %w", err)
			}
		}
	}
	participantsIndexText := formatEmailParticipantsForSearchIndex(parsed)
	if _, err := tx.ExecContext(ctx, `insert into messages_fts(rowid, id, subject, body, participants) values (?, ?, ?, ?, ?)`, messageRowID, identifier, parsed.EmailSubject, parsed.EmailBodyText, participantsIndexText); err != nil {
		return fmt.Errorf("index message: %w", err)
	}
	for _, attachment := range parsed.EmailAttachments {
		if _, err := tx.ExecContext(ctx, `insert into attachments(message_id, filename, mime_type, size_bytes) values (?, ?, ?, ?)`,
			identifier, attachment.AttachmentFilename, attachment.AttachmentMediaType, attachment.AttachmentDecodedByteCount,
		); err != nil {
			return fmt.Errorf("insert attachment: %w", err)
		}
	}
	return insertMessageParticipants(ctx, tx, identifier, parsed)
}

// ArchivedGmailMessageIdentifiers returns which of identifiers the archive
// already holds, live or deleted from Gmail. Their raw bytes are stored and
// never change, so Gmail is asked only for their labels.
func (s *Store) ArchivedGmailMessageIdentifiers(ctx context.Context, identifiers []gmailapi.GmailMessageIdentifier) (map[gmailapi.GmailMessageIdentifier]struct{}, error) {
	archived := map[gmailapi.GmailMessageIdentifier]struct{}{}
	for start := 0; start < len(identifiers); start += 500 {
		chunk := identifiers[start:min(start+500, len(identifiers))]
		arguments := make([]any, 0, len(chunk))
		for _, identifier := range chunk {
			arguments = append(arguments, identifier)
		}
		rows, err := s.store.DB().QueryContext(ctx, `select id from messages where id in (`+placeholders(len(chunk))+`)`, arguments...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var identifier gmailapi.GmailMessageIdentifier
			if err := rows.Scan(&identifier); err != nil {
				_ = rows.Close()
				return nil, err
			}
			archived[identifier] = struct{}{}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return archived, nil
}

// ApplyGmailMailboxChanges writes label changes and deletions with the next
// history cursor in one transaction. When it finishes a full listing, the
// listing tables are cleared in the same transaction.
func (s *Store) ApplyGmailMailboxChanges(ctx context.Context, changes GmailMailboxChangesToApply) (int, error) {
	deleted := 0
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, labelState := range changes.GmailMessageLabelStates {
			if err := writeGmailMessageLabelState(ctx, tx, labelState); err != nil {
				return err
			}
		}
		for _, identifier := range changes.GmailMessagesDeletedFromSource {
			messageWasDeleted, err := markGmailMessageDeletedFromSource(ctx, tx, identifier, changes.DeletedFromSourceTime)
			if err != nil {
				return err
			}
			if messageWasDeleted {
				deleted++
			}
		}
		if changes.FinishesFullMailboxListing {
			if err := clearFullMailboxListing(ctx, tx, true); err != nil {
				return err
			}
		}
		return state.New(tx).Set(ctx, sourceName, mailboxEntityType, gmailHistoryCursorEntityID, string(changes.NextGmailHistoryCursor))
	})
	return deleted, err
}

func writeGmailMessageLabelState(ctx context.Context, tx *sql.Tx, labelState gmailapi.GmailMessageLabelState) error {
	if _, err := tx.ExecContext(ctx, `update messages set labels_json = ?, is_unread = ?, deleted_from_source_at = '' where id = ?`,
		labelsJSON(labelState.GmailLabelIdentifiers), gmailLabelsMarkMessageUnread(labelState.GmailLabelIdentifiers), labelState.GmailMessageIdentifier,
	); err != nil {
		return fmt.Errorf("update message labels: %w", err)
	}
	return nil
}

func markGmailMessageDeletedFromSource(ctx context.Context, tx *sql.Tx, identifier gmailapi.GmailMessageIdentifier, deletedFromSourceTime time.Time) (bool, error) {
	result, err := tx.ExecContext(ctx, `update messages set deleted_from_source_at = ? where id = ? and deleted_from_source_at = ''`,
		deletedFromSourceTime.UTC().Format(time.RFC3339), identifier)
	if err != nil {
		return false, fmt.Errorf("mark message deleted from Gmail: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func (s *Store) GmailHistoryCursor(ctx context.Context) (gmailapi.GmailHistoryIdentifier, error) {
	record, found, err := state.New(s.store.DB()).Get(ctx, sourceName, mailboxEntityType, gmailHistoryCursorEntityID)
	if err != nil || !found {
		return "", err
	}
	return gmailapi.GmailHistoryIdentifier(record.Value), nil
}

func (s *Store) WriteGmailLabels(ctx context.Context, labels []gmailapi.GmailLabel) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from gmail_labels`); err != nil {
			return err
		}
		for _, label := range labels {
			if _, err := tx.ExecContext(ctx, `insert into gmail_labels(id, name, type) values (?, ?, ?)`,
				label.GmailLabelIdentifier, label.GmailLabelName, label.GmailLabelType); err != nil {
				return fmt.Errorf("write Gmail label: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) ArchivedGmailMessagesWithOlderParse(ctx context.Context, currentEmailParserRevision string) ([]ArchivedGmailMessageWithOlderParse, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select id, raw_email_original_sha256 from messages where email_parser_revision <> ?`, currentEmailParserRevision)
	if err != nil {
		return nil, fmt.Errorf("read messages with an older parse: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var messages []ArchivedGmailMessageWithOlderParse
	for rows.Next() {
		var message ArchivedGmailMessageWithOlderParse
		if err := rows.Scan(&message.GmailMessageIdentifier, &message.RawEmailOriginalSHA256); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// RewriteReparsedGmailMessages keeps each row's Gmail facts: labels, time
// and whether it is still in Gmail.
func (s *Store) RewriteReparsedGmailMessages(ctx context.Context, messages []ReparsedGmailMessage) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, message := range messages {
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
				message.GmailMessageIdentifier,
			); err != nil {
				return fmt.Errorf("rewrite parsed message: %w", err)
			}
			if err := replaceDerivedMessageRows(ctx, tx, message.GmailMessageIdentifier, false, parsed); err != nil {
				return err
			}
		}
		return nil
	})
}

func assignShortReferences(ctx context.Context, tx *sql.Tx, identifiers []gmailapi.GmailMessageIdentifier) error {
	candidates := make([]trawlkit.ShortReferenceAssignmentCandidate, 0, len(identifiers))
	for _, identifier := range identifiers {
		candidates = append(candidates, trawlkit.ShortReferenceAssignmentCandidate{
			StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(RefPrefix + string(identifier)),
		})
	}
	return trawlkit.AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(ctx, tx, candidates)
}

func (s *Store) MarkUpdateStarted(ctx context.Context, when time.Time) error {
	return state.New(s.store.DB()).Set(ctx, sourceName, updateEntityType, lastStartedEntityID, when.UTC().Format(time.RFC3339))
}

func (s *Store) MarkUpdateCompleted(ctx context.Context, when time.Time) error {
	return state.New(s.store.DB()).Set(ctx, sourceName, updateEntityType, lastDoneEntityID, when.UTC().Format(time.RFC3339))
}

func (s *Store) UpdateMarkers(ctx context.Context) (UpdateMarkers, error) {
	stateStore := state.New(s.store.DB())
	started, hasStarted, err := stateStore.Get(ctx, sourceName, updateEntityType, lastStartedEntityID)
	if err != nil {
		return UpdateMarkers{}, err
	}
	done, hasDone, err := stateStore.Get(ctx, sourceName, updateEntityType, lastDoneEntityID)
	if err != nil {
		return UpdateMarkers{}, err
	}
	markers := UpdateMarkers{HasCompleted: hasDone}
	if hasDone {
		markers.LastCompletedAt = recordTime(done)
	}
	if hasStarted && (!hasDone || recordTime(started).After(recordTime(done))) {
		markers.PreviousRunIncomplete = true
	}
	return markers, nil
}

func recordTime(rec state.Record) time.Time {
	parsed, err := time.Parse(time.RFC3339, rec.Value)
	if err == nil {
		return parsed
	}
	return rec.UpdatedAt
}

func formatEmailParticipantsForSearchIndex(parsed mailparse.ParsedEmailMessage) string {
	parts := []string{
		strings.TrimSpace(parsed.SenderEmailAddress.DisplayName),
		string(parsed.SenderEmailAddress.NormalizedEmailAddress),
		mailparse.FormatEmailAddressList(parsed.RecipientEmailAddresses),
		mailparse.FormatEmailAddressList(parsed.CopiedRecipientEmailAddresses),
	}
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, " ")
}

func joinNormalizedEmailAddresses(addresses []mailparse.NormalizedEmailAddress) string {
	texts := make([]string, 0, len(addresses))
	for _, address := range addresses {
		texts = append(texts, string(address))
	}
	return strings.Join(texts, ", ")
}

func gmailLabelsMarkMessageUnread(labelIdentifiers []gmailapi.GmailLabelIdentifier) bool {
	for _, labelIdentifier := range labelIdentifiers {
		if labelIdentifier == gmailapi.GmailUnreadLabelIdentifier {
			return true
		}
	}
	return false
}
