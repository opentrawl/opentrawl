package archive

import (
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/gmailapi"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

// ArchivedGmailMessage is one parsed Gmail message ready to write.
type ArchivedGmailMessage struct {
	GmailMessageIdentifier gmailapi.GmailMessageIdentifier
	GmailThreadIdentifier  gmailapi.GmailThreadIdentifier
	GmailHistoryIdentifier gmailapi.GmailHistoryIdentifier
	// GmailInternalDateMilliseconds is Gmail's internalDate, or the Date
	// header when Gmail reported none, or 0 when neither is known.
	GmailInternalDateMilliseconds int64
	GmailLabelIdentifiers         []gmailapi.GmailLabelIdentifier
	RawEmailOriginalSHA256        mailparse.RawEmailOriginalSHA256
	EmailParserRevision           string
	ParsedEmailMessage            mailparse.ParsedEmailMessage
}

// ArchivedGmailMessageWithOlderParse is a message parsed by an earlier
// parser revision. Its stored original is parsed again.
type ArchivedGmailMessageWithOlderParse struct {
	GmailMessageIdentifier gmailapi.GmailMessageIdentifier
	RawEmailOriginalSHA256 mailparse.RawEmailOriginalSHA256
}

type ReparsedGmailMessage struct {
	GmailMessageIdentifier gmailapi.GmailMessageIdentifier
	EmailParserRevision    string
	ParsedEmailMessage     mailparse.ParsedEmailMessage
}

// GmailMailboxChangesToApply is the end of one incremental update: label
// changes, messages that left Gmail, and the cursor the next update starts
// from, written in one transaction.
type GmailMailboxChangesToApply struct {
	GmailMessageLabelStates        []gmailapi.GmailMessageLabelState
	GmailMessagesDeletedFromSource []gmailapi.GmailMessageIdentifier
	NextGmailHistoryCursor         gmailapi.GmailHistoryIdentifier
	FinishesFullMailboxListing     bool
	DeletedFromSourceTime          time.Time
}

type Attachment struct {
	Filename string
	MIMEType string
	Size     int64
}

type Status struct {
	ArchivePath     string
	ArchiveBytes    int64
	LastUpdateAt    string
	NewestMessageAt time.Time
	Messages        int64
}

type SearchOptions struct {
	Query         string
	Limit         int
	BoundedTotals bool
	After         *time.Time
	Before        *time.Time
	PersonFilter  trawlkit.SearchPersonFilter
}

type SearchResult struct {
	Query             string
	WhoResolved       *WhoResolved
	WhoQuery          string
	Results           []SearchHit
	TotalMatches      int64
	TotalIsLowerBound bool
	Truncated         bool
}

type SearchHit struct {
	Ref      string
	Time     string
	Who      string
	Where    string
	Snippet  string
	ShortRef string
	Unread   bool
	Matches  []SearchMatch
}

type SearchMatch struct {
	Field string
	Runs  []store.FTS5TextRun
}

type OpenResult struct {
	Ref                   string
	ID                    string
	ThreadID              string
	Time                  string
	Headers               MailHeaders
	LabelNames            []string
	Unread                bool
	Attachments           []Attachment
	Body                  string
	DeletedFromSourceTime time.Time
}

type MailHeaders struct {
	FromName                    string
	FromAddress                 string
	ToAddress                   string
	CcAddress                   string
	ReplyToAddress              string
	MailingListIdentifier       string
	RFC5322MessageIdentifier    string
	InReplyToMessageIdentifiers string
	Subject                     string
}

type WhoResult struct {
	Query      string
	Candidates []WhoCandidate
}

type WhoCandidate struct {
	Who         string
	Identifiers []string
	LastSeen    string
	Messages    int64

	participantKeys []string
	matchRank       whomatch.Rank
	lastSeenUnix    int64
	messageIDs      []string
}

type WhoResolved struct {
	Who         string
	Identifiers []string
}
