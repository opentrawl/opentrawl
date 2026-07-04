package archive

import (
	"time"

	"github.com/openclaw/crawlkit/whomatch"
)

type Message struct {
	ID             string
	ThreadID       string
	HistoryID      string
	InternalDateMS int64
	Time           time.Time
	FromName       string
	FromAddress    string
	ToAddress      string
	CcAddress      string
	Subject        string
	Body           string
	Labels         []string
	Attachments    []Attachment
}

type Attachment struct {
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

type InsertResult struct {
	Seen     int
	Inserted int
}

type SyncMarkers struct {
	HasCompleted          bool
	PreviousRunIncomplete bool
	LastCompletedAt       time.Time
}

type Status struct {
	ArchivePath  string
	ArchiveBytes int64
	LastSyncAt   string
	Messages     int64
	Senders      int64
	Since        int64
}

type SearchOptions struct {
	Query  string
	Limit  int
	After  *time.Time
	Before *time.Time
	Who    string
}

type SearchResult struct {
	Query        string       `json:"query"`
	WhoResolved  *WhoResolved `json:"who_resolved,omitempty"`
	WhoQuery     string       `json:"-"`
	Results      []SearchHit  `json:"results"`
	TotalMatches int64        `json:"total_matches"`
	Truncated    bool         `json:"truncated"`
}

type SearchHit struct {
	Ref      string `json:"ref"`
	Time     string `json:"time"`
	Who      string `json:"who"`
	Where    string `json:"where,omitempty"`
	Snippet  string `json:"snippet"`
	ShortRef string `json:"short_ref"`
}

type OpenResult struct {
	Ref             string       `json:"ref"`
	ID              string       `json:"id"`
	ThreadID        string       `json:"thread_id"`
	Time            string       `json:"time"`
	Headers         MailHeaders  `json:"headers"`
	Labels          []string     `json:"labels,omitempty"`
	Attachments     []Attachment `json:"attachments,omitempty"`
	Body            string       `json:"body"`
	BodyTruncated   bool         `json:"body_truncated"`
	BodyElidedChars int          `json:"body_elided_chars,omitempty"`
}

type MailHeaders struct {
	FromName    string `json:"from_name,omitempty"`
	FromAddress string `json:"from_address,omitempty"`
	ToAddress   string `json:"to_address"`
	CcAddress   string `json:"cc_address,omitempty"`
	Subject     string `json:"subject"`
}

type WhoResult struct {
	Query      string         `json:"query"`
	Candidates []WhoCandidate `json:"candidates"`
}

type WhoCandidate struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
	LastSeen    string   `json:"last_seen"`
	Messages    int64    `json:"messages"`

	participantKeys []string
	matchRank       whomatch.Rank
	lastSeenUnix    int64
	messageIDs      []string
}

type WhoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type BackupShardKind string

const (
	BackupShardMessages BackupShardKind = "messages"
	BackupShardLabels   BackupShardKind = "labels"
)

type BackupShard struct {
	Path string
	Hash string
	Kind BackupShardKind
	Rows int64
}

type IngestResult struct {
	Shard        BackupShard
	Seen         int
	Inserted     int
	Labels       int
	ParseElapsed time.Duration
	IndexElapsed time.Duration
}
