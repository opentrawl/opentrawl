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

type Info struct {
	ID          string
	Surface     string
	Aliases     []string
	DisplayName string
	Description string
	// ShortRefs declares that the generated manifest includes the
	// "short_refs" capability. Consumers key on that manifest capability,
	// so crawlkit can change this declaration mechanism without changing
	// the wire contract.
	ShortRefs bool
	Privacy   control.Privacy
	// DefaultPaths overrides the runner's default per-crawler paths when a
	// crawler owns a non-SQLite archive or an existing state layout.
	DefaultPaths Paths
	Config       any
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
	// Store declares archive access. StoreDefault keeps the runner default.
	Store   StoreAccess
	Timeout time.Duration
	Run     func(ctx context.Context, req *Request) error
}

// StoreAccess declares how a verb opens the archive store.
type StoreAccess int

const (
	// StoreDefault keeps the verb's runner default.
	StoreDefault StoreAccess = iota
	// StoreNone runs without opening or creating an archive.
	StoreNone
	// StoreOptional opens the archive read-only when it exists. It is only
	// valid on non-mutating verbs.
	StoreOptional
	// StoreRequired opens a bespoke verb's archive, read-only for
	// non-mutating verbs and read-write for mutating verbs.
	StoreRequired
)

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
	Source       string    `json:"source,omitempty"`
	Ref          string    `json:"ref"`
	ShortRef     string    `json:"short_ref,omitempty"`
	Time         time.Time `json:"time"`
	Who          string    `json:"who,omitempty"`
	Where        string    `json:"where,omitempty"`
	Calendar     string    `json:"calendar,omitempty"`
	Snippet      string    `json:"snippet,omitempty"`
	AllDay       bool      `json:"all_day,omitempty"`
	Availability *int64    `json:"availability,omitempty"`
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
