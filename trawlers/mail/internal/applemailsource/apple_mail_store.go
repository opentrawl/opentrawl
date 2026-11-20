// Package applemailsource reads Apple Mail's local store read-only: the
// Envelope Index database, the Accounts4 database and the .emlx message files.
package applemailsource

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type AppleMailMessageRowIdentifier int64

type AppleMailMailboxRowIdentifier int64

// AppleMailAccountIdentifier is the account UUID that names an account
// folder under ~/Library/Mail/V<N> and the host of its mailbox URLs.
type AppleMailAccountIdentifier string

type AppleMailMailboxURL string

type AppleMailStoreLocation struct {
	AppleMailVersionDirectoryPath string
	EnvelopeIndexDatabasePath     string
	AccountsDatabasePath          string
}

// LocateAppleMailStore finds the highest ~/Library/Mail/V<N> folder. Without
// Full Disk Access the directory listing fails with os.ErrPermission.
func LocateAppleMailStore(homeDirectoryPath string) (AppleMailStoreLocation, error) {
	mailDirectoryPath := filepath.Join(homeDirectoryPath, "Library", "Mail")
	entries, err := os.ReadDir(mailDirectoryPath)
	if err != nil {
		return AppleMailStoreLocation{}, fmt.Errorf("list Apple Mail folder: %w", err)
	}
	highestVersion := -1
	for _, entry := range entries {
		versionText, isVersionDirectory := strings.CutPrefix(entry.Name(), "V")
		version, err := strconv.Atoi(versionText)
		if !isVersionDirectory || err != nil || !entry.IsDir() {
			continue
		}
		highestVersion = max(highestVersion, version)
	}
	if highestVersion < 0 {
		return AppleMailStoreLocation{}, fmt.Errorf("no Apple Mail store in %s: %w", mailDirectoryPath, os.ErrNotExist)
	}
	versionDirectoryPath := filepath.Join(mailDirectoryPath, "V"+strconv.Itoa(highestVersion))
	return AppleMailStoreLocation{
		AppleMailVersionDirectoryPath: versionDirectoryPath,
		EnvelopeIndexDatabasePath:     filepath.Join(versionDirectoryPath, "MailData", "Envelope Index"),
		AccountsDatabasePath:          filepath.Join(homeDirectoryPath, "Library", "Accounts", "Accounts4.sqlite"),
	}, nil
}

type AppleMailMailbox struct {
	AppleMailMailboxRowIdentifier AppleMailMailboxRowIdentifier
	AppleMailMailboxURL           AppleMailMailboxURL
	AppleMailAccountIdentifier    AppleMailAccountIdentifier
	// AppleMailMailboxName is the decoded mailbox path, such as "INBOX" or
	// "[Gmail]/All Mail".
	AppleMailMailboxName string
}

// junkOrTrashMailboxNames are the folder names mail servers use for spam and
// deleted mail. IMAP special-use flags are not usable: iCloud's Junk folder
// carries none in Apple Mail's mailbox cache.
var junkOrTrashMailboxNames = map[string]struct{}{
	"junk": {}, "junk e-mail": {}, "junk email": {}, "spam": {}, "bulk mail": {},
	"trash": {}, "bin": {}, "deleted messages": {}, "deleted items": {},
}

var sentMailboxNames = map[string]struct{}{
	"sent": {}, "sent messages": {}, "sent items": {}, "sent mail": {},
}

func (mailbox AppleMailMailbox) IsJunkOrTrashMailbox() bool {
	_, found := junkOrTrashMailboxNames[mailbox.lastMailboxNameComponent()]
	return found
}

func (mailbox AppleMailMailbox) IsSentMailbox() bool {
	_, found := sentMailboxNames[mailbox.lastMailboxNameComponent()]
	return found
}

// IsGmailSystemMailbox reports Gmail's IMAP system folder tree, which Gmail
// names "[Gmail]" or, in some countries, "[Google Mail]".
func (mailbox AppleMailMailbox) IsGmailSystemMailbox() bool {
	firstComponent, _, _ := strings.Cut(mailbox.AppleMailMailboxName, "/")
	return firstComponent == "[Gmail]" || firstComponent == "[Google Mail]"
}

func (mailbox AppleMailMailbox) lastMailboxNameComponent() string {
	name := mailbox.AppleMailMailboxName
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		name = name[index+1:]
	}
	return strings.ToLower(strings.TrimSpace(name))
}

type AppleMailEnvelopeMessage struct {
	AppleMailMessageRowIdentifier AppleMailMessageRowIdentifier
	AppleMailMailboxRowIdentifier AppleMailMailboxRowIdentifier
	AppleMailMessageIsRead        bool
	AppleMailMessageReceivedTime  time.Time
}

type AppleMailEnvelopeIndexContents struct {
	AppleMailMailboxesByRowIdentifier map[AppleMailMailboxRowIdentifier]AppleMailMailbox
	// AppleMailMessagesNotDeleted excludes rows Apple Mail marks deleted.
	AppleMailMessagesNotDeleted []AppleMailEnvelopeMessage
}

// ReadAppleMailEnvelopeIndex reads the live database in one read transaction
// without creating lock files or writing its shared-memory index, so Mail.app
// is never blocked or changed.
func ReadAppleMailEnvelopeIndex(ctx context.Context, envelopeIndexDatabasePath string) (AppleMailEnvelopeIndexContents, error) {
	envelopeIndex, err := store.OpenReadOnlyWithoutWritingFiles(ctx, envelopeIndexDatabasePath)
	if err != nil {
		return AppleMailEnvelopeIndexContents{}, fmt.Errorf("open Apple Mail Envelope Index: %w", err)
	}
	defer func() { _ = envelopeIndex.Close() }()
	readTransaction, err := envelopeIndex.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AppleMailEnvelopeIndexContents{}, fmt.Errorf("begin Apple Mail Envelope Index read: %w", err)
	}
	defer func() { _ = readTransaction.Rollback() }()
	contents := AppleMailEnvelopeIndexContents{AppleMailMailboxesByRowIdentifier: map[AppleMailMailboxRowIdentifier]AppleMailMailbox{}}
	mailboxRows, err := readTransaction.QueryContext(ctx, `select ROWID, url from mailboxes`)
	if err != nil {
		return AppleMailEnvelopeIndexContents{}, fmt.Errorf("read Apple Mail mailboxes: %w", err)
	}
	for mailboxRows.Next() {
		var mailboxRowIdentifier AppleMailMailboxRowIdentifier
		var mailboxURL string
		if err := mailboxRows.Scan(&mailboxRowIdentifier, &mailboxURL); err != nil {
			_ = mailboxRows.Close()
			return AppleMailEnvelopeIndexContents{}, err
		}
		contents.AppleMailMailboxesByRowIdentifier[mailboxRowIdentifier] = newAppleMailMailbox(mailboxRowIdentifier, mailboxURL)
	}
	if err := errorsFromRows(mailboxRows); err != nil {
		return AppleMailEnvelopeIndexContents{}, err
	}
	messageRows, err := readTransaction.QueryContext(ctx, `
select ROWID, mailbox, read, coalesce(date_received, 0)
from messages
where deleted = 0`)
	if err != nil {
		return AppleMailEnvelopeIndexContents{}, fmt.Errorf("read Apple Mail messages: %w", err)
	}
	for messageRows.Next() {
		var message AppleMailEnvelopeMessage
		var receivedUnixSeconds int64
		if err := messageRows.Scan(&message.AppleMailMessageRowIdentifier, &message.AppleMailMailboxRowIdentifier, &message.AppleMailMessageIsRead, &receivedUnixSeconds); err != nil {
			_ = messageRows.Close()
			return AppleMailEnvelopeIndexContents{}, err
		}
		if receivedUnixSeconds > 0 {
			message.AppleMailMessageReceivedTime = time.Unix(receivedUnixSeconds, 0)
		}
		contents.AppleMailMessagesNotDeleted = append(contents.AppleMailMessagesNotDeleted, message)
	}
	if err := errorsFromRows(messageRows); err != nil {
		return AppleMailEnvelopeIndexContents{}, err
	}
	return contents, nil
}

func newAppleMailMailbox(mailboxRowIdentifier AppleMailMailboxRowIdentifier, mailboxURL string) AppleMailMailbox {
	mailbox := AppleMailMailbox{AppleMailMailboxRowIdentifier: mailboxRowIdentifier, AppleMailMailboxURL: AppleMailMailboxURL(mailboxURL)}
	parsedURL, err := url.Parse(mailboxURL)
	if err != nil {
		return mailbox
	}
	mailbox.AppleMailAccountIdentifier = AppleMailAccountIdentifier(parsedURL.Host)
	mailbox.AppleMailMailboxName = strings.TrimPrefix(parsedURL.Path, "/")
	return mailbox
}

func errorsFromRows(rows *sql.Rows) error {
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil {
		return rowsErr
	}
	return closeErr
}
