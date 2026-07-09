package trawlkit

import (
	"io"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/store"
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
	// Kind declares a short-ref kind to clear even when this record carries no
	// Ref.
	//
	// A rebuild deletes and rebuilds exactly the declared kinds. The declared set
	// must cover every kind ShortRefRecords can emit. Omit a kind and stale rows
	// for that kind survive forever when an archive becomes empty; declare a kind
	// the crawler does not regenerate and sync wipes live rows for it.
	Kind string
}
