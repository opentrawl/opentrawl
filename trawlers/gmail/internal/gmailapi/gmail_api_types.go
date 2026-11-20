// Package gmailapi reads a mailbox through the Gmail REST API with the
// gmail.readonly scope.
package gmailapi

import "errors"

type GmailMessageIdentifier string

type GmailThreadIdentifier string

// GmailHistoryIdentifier is a point in a mailbox's history. The mailbox's
// current one is the cursor that incremental updates start from.
type GmailHistoryIdentifier string

type GmailLabelIdentifier string

// Gmail system labels this trawler acts on.
const (
	GmailUnreadLabelIdentifier GmailLabelIdentifier = "UNREAD"
	GmailSentLabelIdentifier   GmailLabelIdentifier = "SENT"
	GmailSpamLabelIdentifier   GmailLabelIdentifier = "SPAM"
	GmailTrashLabelIdentifier  GmailLabelIdentifier = "TRASH"
)

// ErrGmailHistoryCursorExpired means Google no longer keeps history from the
// cursor (HTTP 404), so the mailbox must be listed again.
var ErrGmailHistoryCursorExpired = errors.New("gmail history cursor expired; the mailbox must be listed again")

type GmailProfile struct {
	GoogleAccountEmailAddress     string
	CurrentGmailHistoryIdentifier GmailHistoryIdentifier
}

type GmailLabel struct {
	GmailLabelIdentifier GmailLabelIdentifier
	GmailLabelName       string
	GmailLabelType       string
}

// FetchedRawGmailMessage is one message in Gmail's raw format: the complete
// RFC 5322 bytes and Gmail's own facts about it.
type FetchedRawGmailMessage struct {
	GmailMessageIdentifier        GmailMessageIdentifier
	GmailThreadIdentifier         GmailThreadIdentifier
	GmailHistoryIdentifier        GmailHistoryIdentifier
	GmailInternalDateMilliseconds int64
	GmailInternalDateWasReported  bool
	GmailLabelIdentifiers         []GmailLabelIdentifier
	RawRFC5322EmailMessage        []byte
}

type GmailMessageLabelState struct {
	GmailMessageIdentifier GmailMessageIdentifier
	GmailLabelIdentifiers  []GmailLabelIdentifier
}

// GmailMailboxChanges is the net effect of the history since a cursor: each
// message is either changed (added, or its labels changed) or deleted,
// according to its last history record.
type GmailMailboxChanges struct {
	ChangedGmailMessageIdentifiers []GmailMessageIdentifier
	DeletedGmailMessageIdentifiers []GmailMessageIdentifier
	NextGmailHistoryCursor         GmailHistoryIdentifier
}

// GmailLabelsExcludeMessageFromArchive reports a message in Spam or Trash.
// The archive keeps only what the mailbox keeps outside those two.
func GmailLabelsExcludeMessageFromArchive(labelIdentifiers []GmailLabelIdentifier) bool {
	for _, labelIdentifier := range labelIdentifiers {
		if labelIdentifier == GmailSpamLabelIdentifier || labelIdentifier == GmailTrashLabelIdentifier {
			return true
		}
	}
	return false
}
