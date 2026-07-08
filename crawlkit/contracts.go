package crawlkit

import (
	"context"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/whomatch"
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

type ShortRefProvider interface {
	ShortRefRecords(ctx context.Context, req *Request) ([]ShortRefRecord, error)
}
