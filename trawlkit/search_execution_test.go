package trawlkit

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

type searcherFunc func(context.Context, *Request, Query) (SearchResult, error)

func (fn searcherFunc) Search(ctx context.Context, req *Request, query Query) (SearchResult, error) {
	return fn(ctx, req, query)
}

type searchLifecycleCrawler struct {
	*testCrawler
	prepareCalls int
}

func (c *searchLifecycleCrawler) PrepareReadArchive(context.Context, string) error {
	c.prepareCalls++
	return nil
}

type deadlineSearchCrawler struct{ *testCrawler }

func (c *deadlineSearchCrawler) Search(ctx context.Context, _ *Request, _ Query) (SearchResult, error) {
	<-ctx.Done()
	return SearchResult{}, nil
}

func TestRunSearchUsesNamespacedReadLifecycle(t *testing.T) {
	ctx := context.Background()
	stateRoot := t.TempDir()
	archive := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	store, err := ckstore.Open(ctx, ckstore.Options{Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	const ref = "testcrawl:item/1"
	req := &Request{Store: store}
	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	crawler := &searchLifecycleCrawler{testCrawler: &testCrawler{searchFn: func(context.Context, *Request, Query) (SearchResult, error) {
		return SearchResult{Results: []Hit{{Ref: ref}}, TotalMatches: 1}, nil
	}}}

	result, err := RunSearch(ctx, crawler, Query{Text: "synthetic", Limit: 1}, SearchRunOptions{
		StateRoot: stateRoot,
		Timeout:   time.Second,
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if crawler.prepareCalls != 1 {
		t.Fatalf("read preparation calls = %d, want 1", crawler.prepareCalls)
	}
	if len(result.Results) != 1 || result.Results[0].ShortRef != aliases[ref] {
		t.Fatalf("result = %#v, want stored alias %q", result, aliases[ref])
	}
}

func TestRunSearchReturnsDeadlineAfterSourceStops(t *testing.T) {
	stateRoot := t.TempDir()
	store, err := ckstore.Open(context.Background(), ckstore.Options{Path: filepath.Join(stateRoot, "testcrawl", "testcrawl.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = RunSearch(context.Background(), &deadlineSearchCrawler{testCrawler: &testCrawler{}}, Query{}, SearchRunOptions{
		StateRoot: stateRoot,
		Timeout:   time.Millisecond,
		Stderr:    io.Discard,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestExecuteSearchCompletesSharedResultFacts(t *testing.T) {
	ctx := context.Background()
	store, err := ckstore.Open(ctx, ckstore.Options{Path: filepath.Join(t.TempDir(), "archive.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	req := &Request{Store: store}
	const ref = "notes:note/1"
	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{ref})
	if err != nil {
		t.Fatal(err)
	}
	resolved := &WhoResolved{Who: "Alex Example", Identifiers: []string{"alex@example.com"}}
	query := Query{Text: "synthetic", WhoResolved: resolved}

	result, err := executeSearch(ctx, searcherFunc(func(gotCtx context.Context, gotReq *Request, gotQuery Query) (SearchResult, error) {
		if gotCtx != ctx || gotReq != req || gotQuery.WhoResolved != resolved {
			t.Fatalf("search input was not preserved")
		}
		return SearchResult{Results: []Hit{{Ref: ref}}, TotalMatches: 1}, nil
	}), req, query)
	if err != nil {
		t.Fatal(err)
	}
	if result.WhoResolved != resolved {
		t.Fatalf("who resolved = %#v, want query resolution", result.WhoResolved)
	}
	if got, want := result.Results[0].ShortRef, aliases[ref]; got != want {
		t.Fatalf("short ref = %q, want %q", got, want)
	}
}

func TestExecuteSearchRejectsImpossibleTotal(t *testing.T) {
	_, err := executeSearch(context.Background(), searcherFunc(func(context.Context, *Request, Query) (SearchResult, error) {
		return SearchResult{Results: []Hit{{Ref: "notes:note/1"}}}, nil
	}), nil, Query{})
	if err == nil || err.Error() != "search total_matches is less than results length" {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteSearchKeepsSourceAliasWhenIndexHasNone(t *testing.T) {
	store, err := ckstore.Open(context.Background(), ckstore.Options{Path: filepath.Join(t.TempDir(), "archive.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	result, err := executeSearch(context.Background(), searcherFunc(func(context.Context, *Request, Query) (SearchResult, error) {
		return SearchResult{Results: []Hit{{Ref: "notes:note/1", ShortRef: "known7"}}, TotalMatches: 1}, nil
	}), &Request{Store: store}, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Results[0].ShortRef != "known7" {
		t.Fatalf("short ref = %q, want source alias", result.Results[0].ShortRef)
	}
}

func TestExecuteSearchPreservesSourceError(t *testing.T) {
	want := errors.New("synthetic search failure")
	_, err := executeSearch(context.Background(), searcherFunc(func(context.Context, *Request, Query) (SearchResult, error) {
		return SearchResult{}, want
	}), nil, Query{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
