package archive

import "time"

type Message struct {
	ID          string
	ThreadID    string
	Time        time.Time
	FromName    string
	FromAddress string
	ToAddress   string
	Subject     string
	Body        string
	Labels      []string
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
}

type SearchResult struct {
	Query        string      `json:"query"`
	Results      []SearchHit `json:"results"`
	TotalMatches int64       `json:"total_matches"`
	Truncated    bool        `json:"truncated"`
}

type SearchHit struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where,omitempty"`
	Snippet string `json:"snippet"`
}

type OpenResult struct {
	Ref      string      `json:"ref"`
	ID       string      `json:"id"`
	ThreadID string      `json:"thread_id"`
	Time     string      `json:"time"`
	Headers  MailHeaders `json:"headers"`
	Labels   []string    `json:"labels,omitempty"`
	Body     string      `json:"body"`
}

type MailHeaders struct {
	FromName    string `json:"from_name,omitempty"`
	FromAddress string `json:"from_address,omitempty"`
	ToAddress   string `json:"to_address"`
	Subject     string `json:"subject"`
}
