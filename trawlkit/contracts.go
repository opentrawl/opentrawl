package trawlkit

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
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

type ChatLister interface {
	Chats(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error)
}

type Opener interface {
	Open(ctx context.Context, req *Request, shortRef string) error
}

type ContactExporter interface {
	ContactExport(ctx context.Context, req *Request) (*control.ContactExport, error)
}

type ShortRefProvider interface {
	ShortRefRecords(ctx context.Context, req *Request) ([]ShortRefRecord, error)
}

// ShortRefKindProvider declares the short-ref kinds a crawler regenerates.
//
// A sync deletes and rebuilds exactly these kinds. The declared set must cover
// every kind ShortRefRecords can emit. Omit a kind and stale rows for that kind
// survive forever when an archive becomes empty; declare a kind the crawler does
// not regenerate and sync wipes live rows for it.
//
// Kind-scoped rebuilds protect archives when the running binary knows fewer
// kinds than a previous binary wrote. They cannot protect binaries built before
// kind-scoped clearing existed: those binaries still clear the whole table, and
// this code cannot retroactively change their compiled behavior.
type ShortRefKindProvider interface {
	ShortRefKinds() []string
}
