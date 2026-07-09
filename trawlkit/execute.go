package trawlkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

func (r runner) runInProcess(ctx context.Context, source Crawler, verb targetVerb, globals globalOptions, format output.Format, wireChild bool) (result executionResult) {
	paths, err := resolveSourcePaths(globals.stateRoot, source.Info())
	if err != nil {
		return executionResult{err: err}
	}
	runLog, err := r.openRunLog(paths, verb, globals, format, wireChild)
	if err != nil {
		return executionResult{err: err}
	}
	if runLog != nil && !wireChild {
		defer func() {
			if err := finishRunLog(runLog, result.err); result.err == nil && err != nil {
				result.err = err
			}
		}()
	}
	if verb.name != "metadata" {
		if err := loadConfig(source.Info(), globals.stateRoot); err != nil {
			return executionResult{err: err}
		}
	}
	if verb.bespoke != nil {
		args, err := parseBespokeFlags(*verb.bespoke, verb.args)
		if err != nil {
			return executionResult{err: err}
		}
		verb.args = args
	}
	if verb.spine != nil {
		args, err := parseSpineFlags(*verb.spine, verb.args, verb.name == "search")
		if err != nil {
			return executionResult{err: err}
		}
		verb.args = args
	}
	if err := validateReadFlags(verb); err != nil {
		return executionResult{err: err}
	}
	var lock *runLock
	if verb.mutates {
		lock, err = acquireRunLock(paths.Base)
		if err != nil {
			return executionResult{err: err}
		}
		defer func() { _ = lock.Close() }()
	}
	var timeout time.Duration
	if !verb.mutates {
		timeout = verb.timeout
		if timeout == 0 && verb.name != "metadata" {
			timeout = r.opts.readTimeout
		}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	st, err := openStore(ctx, paths.Paths, verb.storeMode)
	if err != nil {
		return executionResult{err: err}
	}
	if st != nil {
		defer func() { _ = st.Close() }()
	}
	var out bytes.Buffer
	req := &Request{
		Store:  st,
		Paths:  paths.Paths,
		Format: format,
		Out:    &out,
		Log:    runLog,
		Progress: func(progress Progress) {
			logProgress(runLog, progress)
		},
	}
	if wireChild {
		req.Progress = func(progress Progress) {
			_ = writeChildFrame(r.opts.stdout, childProgressFrame(progress))
		}
	}
	if err := executeVerb(ctx, source, verb, req, globals, format); err != nil {
		return executionResult{output: out.Bytes(), err: err}
	}
	return executionResult{output: out.Bytes()}
}

func (r runner) openRunLog(paths sourcePaths, verb targetVerb, globals globalOptions, format output.Format, attach bool) (*cklog.Run, error) {
	if verb.name == "metadata" {
		return nil, nil
	}
	opts := cklog.Options{
		StateRoot:    paths.StateRoot,
		CrawlerID:    paths.CrawlerID,
		RunID:        globals.runID,
		Command:      verb.name,
		Version:      buildVersion,
		Stderr:       r.opts.stderr,
		Verbosity:    globals.verbosity,
		JSONProgress: format == output.JSON,
	}
	if attach {
		opts.Stderr = &childLogFrameWriter{w: r.opts.stdout}
		if opts.Verbosity < 1 {
			opts.Verbosity = 1
		}
		return cklog.AttachRun(opts)
	}
	return cklog.NewRun(opts)
}

func finishRunLog(runLog *cklog.Run, err error) error {
	if runLog == nil {
		return nil
	}
	if exitCodeFor(err) == 2 {
		return runLog.FinishRejected()
	}
	return runLog.Finish(err)
}

func logProgress(runLog *cklog.Run, progress Progress) {
	if runLog == nil {
		return
	}
	parts := []string{"done=" + strconv.FormatInt(progress.Done, 10)}
	if progress.Total > 0 {
		parts = append(parts, "total="+strconv.FormatInt(progress.Total, 10))
	}
	if message := strings.Join(strings.Fields(progress.Message), " "); message != "" {
		parts = append(parts, "message="+strconv.Quote(message))
	}
	_ = runLog.Info(progressLogEvent(progress.Phase), strings.Join(parts, " "))
}

func progressLogEvent(phase string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(phase)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case b.Len() > 0:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	event := strings.Trim(b.String(), "_")
	if event == "" || event[0] < 'a' || event[0] > 'z' {
		event = "progress"
	}
	if !strings.HasSuffix(event, "_progress") {
		event += "_progress"
	}
	return event
}

func openStore(ctx context.Context, paths Paths, mode storeMode) (*store.Store, error) {
	switch mode {
	case storeNone:
		return nil, nil
	case storeOptional:
		exists, err := pathExists(paths.Archive)
		if err != nil {
			return nil, fmt.Errorf("stat archive: %w", err)
		}
		if !exists {
			return nil, nil
		}
		return store.OpenReadOnly(ctx, paths.Archive)
	case storeRead:
		exists, err := pathExists(paths.Archive)
		if err != nil {
			return nil, fmt.Errorf("stat archive: %w", err)
		}
		if !exists {
			return nil, output.UsageError{Err: fmt.Errorf("archive does not exist at %s", paths.Archive)}
		}
		return store.OpenReadOnly(ctx, paths.Archive)
	case storeWrite:
		return store.Open(ctx, store.Options{Path: paths.Archive})
	default:
		return nil, fmt.Errorf("unknown store mode %d", mode)
	}
}

func executeVerb(ctx context.Context, source Crawler, verb targetVerb, req *Request, globals globalOptions, format output.Format) error {
	if len(verb.args) > 0 && verb.name != "search" && verb.name != "open" && verb.name != "who" && verb.name != "chats" && verb.bespoke == nil {
		return usageError{err: fmt.Errorf("%s takes no arguments", verb.name)}
	}
	switch verb.name {
	case "metadata":
		manifest, err := generateManifest(source, globals.stateRoot, filepathBase(os.Args[0]))
		if err != nil {
			return err
		}
		return writeResult(req.Out, format, "metadata", manifest)
	case "status":
		status, err := source.Status(ctx, req)
		if err != nil {
			return err
		}
		return writeResult(req.Out, format, "status", status)
	case "doctor":
		doctor, err := source.Doctor(ctx, req)
		if err != nil {
			return err
		}
		return writeResult(req.Out, format, "doctor", doctor)
	case "sync":
		report, syncErr := source.(Syncer).Sync(ctx, req)
		rebuildErr := rebuildSourceShortRefs(ctx, source, req)
		if syncErr != nil {
			if rebuildErr != nil {
				return errors.Join(syncErr, rebuildErr)
			}
			return syncErr
		}
		if rebuildErr != nil {
			return rebuildErr
		}
		return writeResult(req.Out, format, "sync", report)
	case "search":
		query, err := parseQuery(verb.args)
		if err != nil {
			return err
		}
		query, err = resolveSearchWho(ctx, source, req, query)
		if err != nil {
			return err
		}
		result, err := source.(Searcher).Search(ctx, req, query)
		if err != nil {
			return err
		}
		if result.WhoResolved == nil && query.WhoResolved != nil {
			result.WhoResolved = query.WhoResolved
		}
		if result.TotalMatches < len(result.Results) {
			return fmt.Errorf("search total_matches is less than results length")
		}
		if err := fillSearchShortRefs(ctx, req, result.Results); err != nil {
			return err
		}
		info := source.Info()
		_, supportsWho := source.(WhoMatcher)
		return writeResult(req.Out, format, "search", searchOutput{Query: query.Text, SourceID: firstText(info.Surface, info.ID), SupportsWho: supportsWho, SearchResult: result})
	case "open":
		if len(verb.args) != 1 {
			return usageError{err: errors.New("open needs one ref")}
		}
		return source.(Opener).Open(ctx, req, verb.args[0])
	case "who":
		if len(verb.args) != 1 {
			return usageError{err: errors.New("who needs one name")}
		}
		candidates, err := source.(WhoMatcher).Who(ctx, req, verb.args[0])
		if err != nil {
			return err
		}
		return writeResult(req.Out, format, "who", newWhoOutput(verb.args[0], candidates))
	case "chats":
		query, err := parseChatQuery(verb.args)
		if err != nil {
			return err
		}
		// Fetch one row past the page so truncation is a fact, not a guess
		// from a full page: the extra row proves more chats exist and is
		// never rendered. Deterministic structural choice, documented here.
		fetch := query
		if !query.All && query.Limit > 0 {
			fetch.Limit = query.Limit + 1
		}
		chats, err := source.(ChatLister).Chats(ctx, req, fetch)
		if err != nil {
			if errors.Is(err, ErrChatsNoReadState) {
				surface := firstText(source.Info().DisplayName, source.Info().Surface, source.Info().ID)
				return output.UsageError{Err: fmt.Errorf("%s stores no read state, so --unread is not available here", surface)}
			}
			return err
		}
		truncated := !query.All && query.Limit > 0 && len(chats) > query.Limit
		if truncated {
			chats = chats[:query.Limit]
		}
		return writeResult(req.Out, format, "chats", newChatsOutput(chats, query.Unread, truncated))
	case "contacts_export":
		contacts, err := source.(ContactExporter).ContactExport(ctx, req)
		if err != nil {
			return err
		}
		if contacts != nil {
			if err := control.ValidateContactExport(*contacts); err != nil {
				return err
			}
		}
		return writeResult(req.Out, format, "contacts", contacts)
	}
	if verb.bespoke == nil || verb.bespoke.Run == nil {
		return usageError{err: fmt.Errorf("unknown verb %q", verb.name)}
	}
	req.Args = verb.args
	if verb.mutates {
		runErr := verb.bespoke.Run(ctx, req)
		rebuildErr := rebuildSourceShortRefs(ctx, source, req)
		if runErr != nil {
			if rebuildErr != nil {
				return errors.Join(runErr, rebuildErr)
			}
			return runErr
		}
		return rebuildErr
	}
	return verb.bespoke.Run(ctx, req)
}

func fillSearchShortRefs(ctx context.Context, req *Request, hits []Hit) error {
	if req == nil || req.Store == nil {
		// Verbs declared StoreNone manage their own storage; there is no
		// runner-owned short-ref index to consult.
		return nil
	}
	refs := make([]string, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, hit.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		return err
	}
	for i := range hits {
		hits[i].ShortRef = aliases[hits[i].Ref]
	}
	return nil
}

func rebuildSourceShortRefs(ctx context.Context, source Crawler, req *Request) error {
	provider, ok := source.(ShortRefProvider)
	if !ok || req.Store == nil {
		return nil
	}
	records, err := provider.ShortRefRecords(ctx, req)
	if err != nil {
		return err
	}
	if _, err := req.RebuildShortRefs(ctx, records); err != nil {
		return err
	}
	if req.Log != nil {
		_ = req.Log.Info("short_refs_rebuilt", fmt.Sprintf("refs=%d", len(records)))
	}
	return nil
}

func validateReadFlags(verb targetVerb) error {
	if verb.name != "search" {
		return nil
	}
	_, err := parseQuery(verb.args)
	return err
}

func resolveSearchWho(ctx context.Context, source Crawler, req *Request, query Query) (Query, error) {
	who := strings.Join(strings.Fields(query.Who), " ")
	if who == "" {
		query.Who = ""
		return query, nil
	}
	matcher, ok := source.(WhoMatcher)
	if !ok {
		return query, output.UsageError{Err: errors.New("--who is not supported by this source")}
	}
	candidates, err := matcher.Who(ctx, req, who)
	if err != nil {
		return query, err
	}
	query.Who = who
	if len(candidates) == 0 {
		return query, whoAmbiguityError{who: who, code: 5}
	}
	if len(candidates) > 1 {
		return query, whoAmbiguityError{query: query.Text, who: who, candidates: candidates, code: 4}
	}
	candidate := candidates[0]
	if rank, ok := candidate.MatchRank(who); ok && rank == whomatch.RankCloseSpelling {
		return query, whoAmbiguityError{who: who, candidates: candidates, code: 5}
	}
	query.WhoResolved = newWhoResolved(candidate)
	return query, nil
}
