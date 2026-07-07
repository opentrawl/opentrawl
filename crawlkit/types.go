package crawlkit

import (
	"context"
	"flag"
	"io"
	"time"

	"github.com/openclaw/crawlkit/control"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/crawlkit/whomatch"
)

const (
	HiddenWireSubcommand = "__crawlkit_wire"
	DefaultReadTimeout   = 2 * time.Minute
	DefaultWatchdog      = 10 * time.Minute
	DefaultKillGrace     = 10 * time.Second
)

type Crawler interface {
	Info() Info
	Status(ctx context.Context, req *Request) (*control.Status, error)
	Doctor(ctx context.Context, req *Request) (*Doctor, error)
	Verbs() []Verb
}

type Syncer interface {
	Sync(ctx context.Context, req *Request) (*SyncReport, error)
}

type Searcher interface {
	Search(ctx context.Context, req *Request, q Query) (SearchResult, error)
}

type WhoMatcher interface {
	Who(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error)
}

type Opener interface {
	Open(ctx context.Context, req *Request, shortRef string) error
}

type ContactExporter interface {
	ContactExport(ctx context.Context, req *Request) (*control.ContactExport, error)
}

type FullCrawler interface {
	Crawler
	Syncer
	Searcher
	WhoMatcher
	Opener
	ContactExporter
}

type Info struct {
	ID          string
	Surface     string
	Aliases     []string
	DisplayName string
	Description string
	Privacy     control.Privacy
	Config      any
}

type Paths struct {
	Archive string
	Config  string
	Logs    string
}

type Request struct {
	Store    *store.Store
	Paths    Paths
	Format   output.Format
	Out      io.Writer
	Args     []string
	Log      *cklog.Run
	Progress func(Progress)
}

type Verb struct {
	Name    string
	Help    string
	Args    []string
	Flags   func(fs *flag.FlagSet)
	Mutates bool
	Timeout time.Duration
	Run     func(ctx context.Context, req *Request) error
}

type Query struct {
	Text          string
	Limit         int
	After, Before time.Time
	Who           string
	WhoResolved   *WhoResolved
}

type WhoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type SearchResult struct {
	WhoResolved  *WhoResolved `json:"who_resolved,omitempty"`
	Results      []Hit        `json:"results"`
	TotalMatches int          `json:"total_matches"`
	Truncated    bool         `json:"truncated"`
}

type Hit struct {
	Source   string    `json:"source,omitempty"`
	Ref      string    `json:"ref"`
	ShortRef string    `json:"short_ref,omitempty"`
	Time     time.Time `json:"time"`
	Who      string    `json:"who,omitempty"`
	Where    string    `json:"where,omitempty"`
	Snippet  string    `json:"snippet,omitempty"`
	AllDay   bool      `json:"all_day,omitempty"`
}

type Doctor struct {
	Checks []Check `json:"checks"`
}

type Check struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
	Remedy  string `json:"remedy,omitempty"`
}

type SyncReport struct {
	Added    int64    `json:"added"`
	Updated  int64    `json:"updated"`
	Removed  int64    `json:"removed"`
	Warnings []string `json:"warnings,omitempty"`
}

type Progress struct {
	Phase   string `json:"phase"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}

type ConfigValidator interface {
	Validate() error
}

type ConfigFieldError struct {
	Field string
	Fix   string
	Err   error
}

func (e ConfigFieldError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Field != "" {
		return "invalid config field " + e.Field
	}
	return "invalid config"
}

func (e ConfigFieldError) Unwrap() error {
	return e.Err
}

func (e ConfigFieldError) ErrorBody() output.ErrorBody {
	body := output.ErrorBody{
		Code:    "config_invalid",
		Message: e.Error(),
		Remedy:  e.Fix,
	}
	if e.Field != "" {
		body.Fields = map[string]any{"field": e.Field}
	}
	return body
}
