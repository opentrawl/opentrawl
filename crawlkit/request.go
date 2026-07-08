package crawlkit

import (
	"io"

	"github.com/openclaw/crawlkit/control"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/store"
)

type Info struct {
	ID          string
	Surface     string
	Aliases     []string
	DisplayName string
	Description string
	Privacy     control.Privacy
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

type ShortRefRecord struct {
	Ref string
}
