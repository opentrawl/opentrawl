package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/gmailapi"
)

// A full mailbox listing runs on the first update and whenever Google no
// longer keeps history from the stored cursor. It records the mailbox's
// history cursor and every listed message, then fetches only messages the
// archive does not hold. Progress survives an interrupted update.

func (s *Store) FullMailboxListingHistoryCursor(ctx context.Context) (gmailapi.GmailHistoryIdentifier, bool, error) {
	var cursor gmailapi.GmailHistoryIdentifier
	err := s.store.DB().QueryRowContext(ctx, `select history_cursor from gmail_full_mailbox_listing_history_cursor where singleton = 1`).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return cursor, err == nil, err
}

// StartFullMailboxListing marks every listed message the archive already
// holds as fetched and still in Gmail, so it is not downloaded again.
func (s *Store) StartFullMailboxListing(ctx context.Context, cursor gmailapi.GmailHistoryIdentifier, listedIdentifiers []gmailapi.GmailMessageIdentifier) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := clearFullMailboxListing(ctx, tx, false); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into gmail_full_mailbox_listing_history_cursor(singleton, history_cursor) values (1, ?)`, cursor); err != nil {
			return fmt.Errorf("start full mailbox listing: %w", err)
		}
		for position, identifier := range listedIdentifiers {
			if _, err := tx.ExecContext(ctx, `insert or ignore into gmail_full_mailbox_listing_messages(id, listing_position) values (?, ?)`, identifier, position); err != nil {
				return fmt.Errorf("record listed message: %w", err)
			}
		}
		_, err := tx.ExecContext(ctx, `
update gmail_full_mailbox_listing_messages set fetched = 1, still_in_gmail = 1
where id in (select id from messages)`)
		return err
	})
}

func (s *Store) GmailMessagesNotYetFetchedInFullMailboxListing(ctx context.Context) ([]gmailapi.GmailMessageIdentifier, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select id from gmail_full_mailbox_listing_messages where fetched = 0 order by listing_position`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var identifiers []gmailapi.GmailMessageIdentifier
	for rows.Next() {
		var identifier gmailapi.GmailMessageIdentifier
		if err := rows.Scan(&identifier); err != nil {
			return nil, err
		}
		identifiers = append(identifiers, identifier)
	}
	return identifiers, rows.Err()
}

// WriteFullMailboxListingBatch writes fetched messages and records the
// batch as done in one transaction. A message Gmail no longer had, or had
// moved to Spam or Trash, is done but not still in Gmail.
func (s *Store) WriteFullMailboxListingBatch(ctx context.Context, messages []ArchivedGmailMessage, excludedIdentifiers []gmailapi.GmailMessageIdentifier) (int, error) {
	added := 0
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		if added, err = writeArchivedGmailMessages(ctx, tx, messages); err != nil {
			return err
		}
		for _, message := range messages {
			if err := markListedMessageFetched(ctx, tx, message.GmailMessageIdentifier, true); err != nil {
				return err
			}
		}
		for _, identifier := range excludedIdentifiers {
			if err := markListedMessageFetched(ctx, tx, identifier, false); err != nil {
				return err
			}
		}
		return nil
	})
	return added, err
}

func markListedMessageFetched(ctx context.Context, tx *sql.Tx, identifier gmailapi.GmailMessageIdentifier, stillInGmail bool) error {
	result, err := tx.ExecContext(ctx, `update gmail_full_mailbox_listing_messages set fetched = 1, still_in_gmail = ? where id = ?`, stillInGmail, identifier)
	if err != nil {
		return fmt.Errorf("record fetched message: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return fmt.Errorf("message %s is not in the full mailbox listing", identifier)
	}
	return nil
}

// ReconcileFullMailboxListing sets the labels of every message still in
// Gmail from Gmail's per-label listings, and marks every other live message
// deleted from Gmail. It does not move the history cursor.
func (s *Store) ReconcileFullMailboxListing(
	ctx context.Context,
	labelsByMessage map[gmailapi.GmailMessageIdentifier][]gmailapi.GmailLabelIdentifier,
	deletedFromSourceTime time.Time,
) (int, error) {
	deleted := int64(0)
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var unfetched int
		if err := tx.QueryRowContext(ctx, `select count(*) from gmail_full_mailbox_listing_messages where fetched = 0`).Scan(&unfetched); err != nil {
			return err
		}
		if unfetched != 0 {
			return fmt.Errorf("full mailbox listing still has %d messages to fetch", unfetched)
		}
		rows, err := tx.QueryContext(ctx, `select id from gmail_full_mailbox_listing_messages where still_in_gmail = 1`)
		if err != nil {
			return err
		}
		var stillInGmail []gmailapi.GmailMessageIdentifier
		for rows.Next() {
			var identifier gmailapi.GmailMessageIdentifier
			if err := rows.Scan(&identifier); err != nil {
				_ = rows.Close()
				return err
			}
			stillInGmail = append(stillInGmail, identifier)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, identifier := range stillInGmail {
			labelState := gmailapi.GmailMessageLabelState{GmailMessageIdentifier: identifier, GmailLabelIdentifiers: labelsByMessage[identifier]}
			if err := writeGmailMessageLabelState(ctx, tx, labelState); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `
update messages set deleted_from_source_at = ?
where deleted_from_source_at = ''
  and not exists (
    select 1 from gmail_full_mailbox_listing_messages listed
    where listed.id = messages.id and listed.still_in_gmail = 1
  )`, deletedFromSourceTime.UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("mark messages deleted from Gmail: %w", err)
		}
		deleted, err = result.RowsAffected()
		return err
	})
	return int(deleted), err
}

func (s *Store) ClearFullMailboxListing(ctx context.Context) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		return clearFullMailboxListing(ctx, tx, false)
	})
}

func clearFullMailboxListing(ctx context.Context, tx *sql.Tx, listingMustExist bool) error {
	result, err := tx.ExecContext(ctx, `delete from gmail_full_mailbox_listing_history_cursor`)
	if err != nil {
		return err
	}
	if listingMustExist {
		if removed, err := result.RowsAffected(); err != nil || removed != 1 {
			return errors.New("no full mailbox listing is in progress")
		}
	}
	_, err = tx.ExecContext(ctx, `delete from gmail_full_mailbox_listing_messages`)
	return err
}
