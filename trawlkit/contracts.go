package trawlkit

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type Crawler interface {
	Info() Info
	// Status reports only OpenTrawl-owned archive state. Source applications,
	// accounts and libraries are inspected by sync, import or doctor.
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

type ChatLister interface {
	Chats(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error)
}

type Opener interface {
	Open(ctx context.Context, req *Request, shortRef string) error
}

type RecordOpener interface {
	OpenRecord(ctx context.Context, req *Request, ref string) (*openv1.OpenRecord, error)
}

// ResourceResolver resolves an opaque, source-owned presentation resource
// within the caller's explicit byte bound. Implementations must not return a
// path or URL in place of the requested bytes.
type ResourceResolver interface {
	ResolveResource(ctx context.Context, req *Request, request *presentationv1.ResourceRequest) (*presentationv1.ResourceResponse, error)
}

type ContactExporter interface {
	ContactExport(ctx context.Context, req *Request) (*control.ContactExport, error)
}

type ShortRefProvider interface {
	ShortRefRecords(ctx context.Context, req *Request) ([]ShortRefRecord, error)
}

// ArchivePreparer lets a crawler peek at its archive file and park an
// out-of-date one aside before the harness opens the long-lived write
// connection (req.Store) the rest of a mutating verb runs against.
//
// Implement this when a crawler owns a self-versioned archive with no
// in-place migration path: something else must decide, before req.Store
// exists, whether the file on disk needs to move aside. The harness calls
// PrepareArchive ahead of opening req.Store for every storeWrite verb, so
// there is no connection yet for the crawler to close or swap out from under
// a verb that keeps running against req.Store after the crawler's own verb
// method returns (see assignSourceShortRefs, which runs Sync's req.Store
// again immediately after Sync itself).
//
// PrepareArchive must not apply schema DDL to a file it might park: doing so
// mutates the very bytes the park is meant to preserve untouched. A crawler
// that has nothing to check can simply not implement this interface.
type ArchivePreparer interface {
	PrepareArchive(ctx context.Context, path string) error
}
