package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	workerv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker/v1"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

// SourceExecutor is the typed host boundary for running crawler operations.
// Every method uses the same paths, configuration, logging, deadline, archive
// preparation and store lifecycle as the source CLI.
type SourceExecutor struct {
	opts SourceExecutorOptions
}

type SourceExecutorOptions struct {
	StateRoot string
	Timeout   time.Duration
	Verbosity int
	Stderr    io.Writer
}

func NewSourceExecutor(opts SourceExecutorOptions) SourceExecutor {
	return SourceExecutor{opts: opts}
}

type typedSourceOperation interface {
	execute(context.Context, Crawler, *Request) error
}

func (e SourceExecutor) runner() runner {
	r := runner{opts: defaultRunOptions()}
	r.opts.stderr = e.opts.Stderr
	r.opts.readTimeout = e.opts.Timeout
	r.opts = r.opts.withDefaults()
	return r
}

func (e SourceExecutor) globals() globalOptions {
	return globalOptions{stateRoot: e.opts.StateRoot, verbosity: e.opts.Verbosity}
}

func (e SourceExecutor) runTyped(ctx context.Context, source Crawler, verb targetVerb, operation typedSourceOperation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	verb.typed = operation
	return e.runner().runInProcess(ctx, source, verb, e.globals(), output.JSON, false).err
}

func typedVerb(source Crawler, name string, args ...string) (targetVerb, error) {
	return resolveVerb(source, append([]string{name}, args...))
}

type typedStatus struct{ result *control.Status }

func (operation *typedStatus) execute(ctx context.Context, source Crawler, req *Request) error {
	status, err := source.Status(ctx, req)
	operation.result = status
	return err
}

func (e SourceExecutor) Status(ctx context.Context, source Crawler) (*control.Status, error) {
	verb, err := typedVerb(source, "status")
	if err != nil {
		return nil, err
	}
	operation := &typedStatus{}
	if err := e.runTyped(ctx, source, verb, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func (e SourceExecutor) Search(ctx context.Context, source Crawler, query Query) (SearchResult, error) {
	verb, err := typedVerb(source, "search")
	if err != nil {
		return SearchResult{}, err
	}
	operation := &typedSearch{query: query}
	if err := e.runTyped(ctx, source, verb, operation); err != nil {
		return SearchResult{}, err
	}
	return operation.result, nil
}

type typedOpenRecord struct {
	ref      string
	anchorID string
	result   *openv1.OpenRecord
}

func (operation *typedOpenRecord) execute(ctx context.Context, source Crawler, req *Request) error {
	opener, ok := source.(RecordOpener)
	if !ok {
		return errors.New("source does not support typed open")
	}
	req.RequestedAnchorID = operation.anchorID
	record, err := opener.OpenRecord(ctx, req, operation.ref)
	operation.result = record
	return err
}

func (e SourceExecutor) OpenRecord(ctx context.Context, source Crawler, ref, anchorID string) (*openv1.OpenRecord, error) {
	if _, ok := source.(RecordOpener); !ok {
		return nil, errors.New("source does not support typed open")
	}
	spine, err := supportedVerbDeclarations(source)
	if err != nil {
		return nil, err
	}
	declaration := spineDeclaration(spine, "open")
	verb := targetVerb{name: "open", args: []string{ref}, spine: declaration, storeMode: spineStoreMode("open", declaration)}
	operation := &typedOpenRecord{ref: ref, anchorID: anchorID}
	if err := e.runTyped(ctx, source, verb, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

type typedWho struct {
	query  string
	result []whomatch.Candidate
}

func (operation *typedWho) execute(ctx context.Context, source Crawler, req *Request) error {
	matcher, ok := source.(WhoMatcher)
	if !ok {
		return errors.New("source does not support who")
	}
	candidates, err := matcher.Who(ctx, req, operation.query)
	operation.result = candidates
	return err
}

func (e SourceExecutor) Who(ctx context.Context, source Crawler, query string) ([]whomatch.Candidate, error) {
	verb, err := typedVerb(source, "who", query)
	if err != nil {
		return nil, err
	}
	operation := &typedWho{query: query}
	if err := e.runTyped(ctx, source, verb, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func (e SourceExecutor) Chats(ctx context.Context, source Crawler, query ChatQuery) (ChatsResult, error) {
	verb, err := typedVerb(source, "chats")
	if err != nil {
		return ChatsResult{}, err
	}
	operation := &typedChats{query: query}
	if err := e.runTyped(ctx, source, verb, operation); err != nil {
		return ChatsResult{}, err
	}
	return operation.result, nil
}

type typedResource struct {
	request *presentationv1.ResourceRequest
	result  *presentationv1.ResourceResponse
}

func (operation *typedResource) execute(ctx context.Context, source Crawler, req *Request) error {
	resolver, ok := source.(ResourceResolver)
	if !ok {
		return errors.New("source does not resolve presentation resources")
	}
	response, err := resolver.ResolveResource(ctx, req, operation.request)
	if err != nil {
		return err
	}
	if err := openrecord.ValidateResourceResponse(operation.request, response); err != nil {
		return err
	}
	operation.result = response
	return nil
}

func (e SourceExecutor) ResolveResource(ctx context.Context, source Crawler, request *presentationv1.ResourceRequest) (*presentationv1.ResourceResponse, error) {
	if err := openrecord.ValidateResourceRequest(request); err != nil {
		return nil, err
	}
	operation := &typedResource{request: request}
	verb := targetVerb{name: "resource", storeMode: storeRead}
	if err := e.runTyped(ctx, source, verb, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

type typedPeopleSnapshot struct{ result *control.PeopleSnapshot }

func (operation *typedPeopleSnapshot) execute(ctx context.Context, source Crawler, req *Request) error {
	provider, ok := source.(PeopleSnapshotProvider)
	if !ok {
		return errors.New("source does not provide People identities")
	}
	snapshot, err := provider.PeopleSnapshot(ctx, req)
	operation.result = snapshot
	return err
}

func (e SourceExecutor) PeopleSnapshot(ctx context.Context, source Crawler) (*control.PeopleSnapshot, error) {
	operation := &typedPeopleSnapshot{}
	verb := targetVerb{name: "people", storeMode: storeRead}
	if err := e.runTyped(ctx, source, verb, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func (e SourceExecutor) ReconcilePeople(ctx context.Context, destination Crawler, source string, snapshot *control.PeopleSnapshot) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := destination.(PeopleReconciler); !ok {
		return errors.New("destination does not own a People archive")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("people source is required")
	}
	if snapshot == nil {
		return errors.New("people snapshot is missing")
	}
	if err := control.ValidatePeopleSnapshot(*snapshot); err != nil {
		return fmt.Errorf("invalid people snapshot: %w", err)
	}
	r := e.runner()
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()
	r.opts.childRequest = &workerv1.Request{Operation: &workerv1.Request_ReconcilePeople{ReconcilePeople: &workerv1.ReconcilePeople{
		Source:   source,
		Snapshot: peopleSnapshotToProto(snapshot),
	}}}
	verb := targetVerb{name: internalPeopleReconcileVerb, mutates: true, storeMode: storeWrite}
	return r.runChild(ctx, destination, verb, e.globals(), output.JSON).err
}

func (e SourceExecutor) Sync(ctx context.Context, source Crawler, args []string) (*SyncReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r := e.runner()
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()
	argv := append([]string{"sync"}, args...)
	result := r.dispatch(ctx, source, argv, e.globals(), output.JSON, false)
	if result.err != nil {
		return nil, result.err
	}
	if result.syncReport == nil {
		return nil, fmt.Errorf("sync child returned no typed sync result")
	}
	return result.syncReport, nil
}
