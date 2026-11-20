package applemailsource

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// googleAccountTypeIdentifier is the macOS Internet Accounts type for a
// Google account. It has not been observed in a real Accounts4 database yet;
// the "[Gmail]" mailbox tree is the observed signal.
const googleAccountTypeIdentifier = "com.apple.account.Google"

type AppleMailAccount struct {
	AppleMailAccountIdentifier AppleMailAccountIdentifier
	// AppleMailAccountDisplayName is the name shown in Mail's sidebar, such
	// as "iCloud" or "On My Mac".
	AppleMailAccountDisplayName string
	// AppleMailAccountUserName is the sign-in name of the account or its
	// parent account. For iCloud it is the Apple ID, which may be a Gmail
	// address, so it does not identify the mail server.
	AppleMailAccountUserName             string
	AppleMailAccountTypeIdentifier       string
	AppleMailParentAccountTypeIdentifier string
}

// IsGmailAccount reports a Gmail account by its Google account type or by
// Gmail's "[Gmail]" system mailbox tree, never by its user name alone.
func (account AppleMailAccount) IsGmailAccount(accountMailboxes []AppleMailMailbox) bool {
	if account.AppleMailAccountTypeIdentifier == googleAccountTypeIdentifier || account.AppleMailParentAccountTypeIdentifier == googleAccountTypeIdentifier {
		return true
	}
	for _, mailbox := range accountMailboxes {
		if mailbox.IsGmailSystemMailbox() {
			return true
		}
	}
	return false
}

func (account AppleMailAccount) NormalizedUserEmailAddress() mailparse.NormalizedEmailAddress {
	if !mailparse.IsEmailAddressText(account.AppleMailAccountUserName) {
		return ""
	}
	return mailparse.NormalizeEmailAddress(account.AppleMailAccountUserName)
}

// ReadAppleMailAccounts reads every account from the live Accounts4 database
// without writing to it. A child account without its own name or user name
// takes them from its parent, as iCloud Mail's IMAP account does.
func ReadAppleMailAccounts(ctx context.Context, accountsDatabasePath string) (map[AppleMailAccountIdentifier]AppleMailAccount, error) {
	accountsDatabase, err := store.OpenReadOnlyWithoutWritingFiles(ctx, accountsDatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open macOS accounts database: %w", err)
	}
	defer func() { _ = accountsDatabase.Close() }()
	rows, err := accountsDatabase.DB().QueryContext(ctx, `
select coalesce(account.ZIDENTIFIER, ''),
       coalesce(account.ZACCOUNTDESCRIPTION, ''), coalesce(account.ZUSERNAME, ''), coalesce(account_type.ZIDENTIFIER, ''),
       coalesce(parent.ZACCOUNTDESCRIPTION, ''), coalesce(parent.ZUSERNAME, ''), coalesce(parent_type.ZIDENTIFIER, '')
from ZACCOUNT account
left join ZACCOUNTTYPE account_type on account_type.Z_PK = account.ZACCOUNTTYPE
left join ZACCOUNT parent on parent.Z_PK = account.ZPARENTACCOUNT
left join ZACCOUNTTYPE parent_type on parent_type.Z_PK = parent.ZACCOUNTTYPE`)
	if err != nil {
		return nil, fmt.Errorf("read macOS accounts: %w", err)
	}
	accounts := map[AppleMailAccountIdentifier]AppleMailAccount{}
	for rows.Next() {
		var account AppleMailAccount
		var accountDescription, accountUserName, parentDescription, parentUserName string
		if err := rows.Scan(
			&account.AppleMailAccountIdentifier,
			&accountDescription, &accountUserName, &account.AppleMailAccountTypeIdentifier,
			&parentDescription, &parentUserName, &account.AppleMailParentAccountTypeIdentifier,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		account.AppleMailAccountDisplayName = firstNonEmptyText(accountDescription, parentDescription, accountUserName, parentUserName)
		account.AppleMailAccountUserName = firstNonEmptyText(accountUserName, parentUserName)
		accounts[account.AppleMailAccountIdentifier] = account
	}
	if err := errorsFromRows(rows); err != nil {
		return nil, err
	}
	return accounts, nil
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
