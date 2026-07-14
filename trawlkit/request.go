package trawlkit

import (
	"context"
	"io"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type internalAppRequestKey struct{}

// WithInternalAppRequest marks a request path that is reachable only through
// the embedded Mac app transport.
func WithInternalAppRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalAppRequestKey{}, true)
}

// IsInternalAppRequest reports whether a request came through the embedded
// Mac app transport.
func IsInternalAppRequest(ctx context.Context) bool {
	marked, _ := ctx.Value(internalAppRequestKey{}).(bool)
	return marked
}

type Info struct {
	ID          string
	Surface     string
	Aliases     []string
	DisplayName string
	Headlines   []string
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
	Store             *store.Store
	Paths             Paths
	Format            output.Format
	Out               io.Writer
	Args              []string
	Log               *cklog.Run
	Progress          func(Progress)
	RequestedAnchorID string
}

type ShortRefRecord struct {
	Ref string
	// CanonicalRef is the ref ResolveShortRef returns for this record's alias.
	// Crawlers set it when Ref is a stable legacy key but open/search now use a
	// newer canonical ref. Empty means Ref. Later syncs update CanonicalRef on
	// existing rows without moving the assigned alias.
	CanonicalRef string
}
