package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type canonicalConsumerObserver interface {
	observeStatus([]federation.StatusSource, *federationv1.StatusResponse)
	observeSearch([]federation.SearchSource, trawlkit.Query, federationv1.SearchOrder, int, *federationv1.SearchResponse)
	observeOpen([]federation.OpenSource, string, string, *openv1.OpenResponse)
	observePresentation(*presentationv1.PresentationDocument)
}

var canonicalJSON = protojson.MarshalOptions{
	UseProtoNames:     true,
	EmitDefaultValues: true,
}

func writeCanonicalJSON(w io.Writer, message proto.Message) error {
	data, err := canonicalJSON.Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func outcomeExit(outcome federationv1.OperationOutcome) error {
	switch outcome {
	case federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE:
		return nil
	case federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL:
		return exitErr{code: 3}
	default:
		return exitErr{code: 1}
	}
}

func (r *Runtime) canonicalStatus(sources []Source) *federationv1.StatusResponse {
	adapters := r.federationStatusSources(sources)
	response := federation.Status(r.ctx, adapters)
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeStatus(adapters, response)
	}
	return response
}

func statusSourceNotFoundResponse(sourceID string) *federationv1.StatusResponse {
	sourceID = strings.TrimSpace(sourceID)
	return &federationv1.StatusResponse{
		Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED,
		Failures: []*federationv1.SourceFailure{{
			SourceId: sourceID,
			Code:     federationv1.FailureCode_FAILURE_CODE_NOT_FOUND,
			Message:  fmt.Sprintf("Source %q was not found.", sourceID),
			Remedy:   "trawl status",
		}},
	}
}

func (r *Runtime) canonicalSearch(sources []federation.SearchSource, query trawlkit.Query, order federationv1.SearchOrder, limit int) *federationv1.SearchResponse {
	response := federation.Search(r.ctx, sources, query, order, uint32(limit))
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeSearch(sources, query, order, limit, response)
	}
	return response
}

func (r *Runtime) canonicalOpen(sources []federation.OpenSource, sourceID, sourceRef, requestedRef string) *openv1.OpenResponse {
	response := federation.Open(r.ctx, sources, sourceID, sourceRef, "")
	response.RequestedRef = requestedRef
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeOpen(sources, sourceID, sourceRef, response)
	}
	return response
}

func statusResultsFromResponse(sources []Source, response *federationv1.StatusResponse) ([]StatusResult, error) {
	statuses := make(map[string]*federationv1.SourceStatus, len(response.GetSources()))
	for _, status := range response.GetSources() {
		statuses[status.GetManifest().GetSourceId()] = status
	}
	failures := make(map[string]*federationv1.SourceFailure, len(response.GetFailures()))
	for _, failure := range response.GetFailures() {
		failures[failure.GetSourceId()] = failure
	}
	skips := make(map[string]*federationv1.SkippedSource, len(response.GetSkippedSources()))
	for _, skip := range response.GetSkippedSources() {
		skips[skip.GetSourceId()] = skip
	}

	results := make([]StatusResult, 0, len(sources))
	for _, source := range sources {
		if status := statuses[source.ID]; status != nil {
			if status.GetManifest() == nil {
				return nil, fmt.Errorf("status response has no source manifest")
			}
			results = append(results, StatusResult{Source: source, Status: status})
			continue
		}
		if failure := failures[source.ID]; failure != nil {
			results = append(results, StatusResult{Source: source, Status: failureStatus(source, failure)})
			continue
		}
		if skip := skips[source.ID]; skip != nil {
			results = append(results, StatusResult{Source: source, Status: skippedStatus(source, skip)})
			continue
		}
		return nil, fmt.Errorf("status response omitted selected source %q", source.ID)
	}
	return results, nil
}

func failureStatus(source Source, failure *federationv1.SourceFailure) *federationv1.SourceStatus {
	return syntheticSourceStatus(source, "error", failure.GetMessage())
}

func skippedStatus(source Source, skip *federationv1.SkippedSource) *federationv1.SourceStatus {
	return syntheticSourceStatus(source, "skipped", skip.GetReason())
}

func syntheticSourceStatus(source Source, state, summary string) *federationv1.SourceStatus {
	return &federationv1.SourceStatus{
		Manifest: &federationv1.SourceManifest{SourceId: source.ID, DisplayName: sourceHumanName(source)},
		AppId:    source.ID, State: state, Summary: summary,
	}
}

func (r *Runtime) reportFederationOutcomes(failures []*federationv1.SourceFailure, skips []*federationv1.SkippedSource, verb string) {
	for _, failure := range failures {
		surface := strings.TrimSpace(failure.GetSurface())
		if surface == "" {
			surface = failure.GetSourceId()
		}
		_, _ = fmt.Fprintf(r.stderr, "%s %s failed: %s\n", surface, verb, strings.TrimSpace(failure.GetMessage()))
		if remedy := normalFailureRemedy(strings.TrimSpace(failure.GetRemedy()), failure.GetSourceId(), verb); remedy != "" {
			_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", remedy)
		}
	}
	for _, skip := range skips {
		surface := strings.TrimSpace(skip.GetSurface())
		if surface == "" {
			surface = skip.GetSourceId()
		}
		_, _ = fmt.Fprintf(r.stderr, "%s %s skipped: %s\n", surface, verb, strings.TrimSpace(skip.GetReason()))
	}
}

func normalFailureRemedy(remedy, source, verb string) string {
	if remedy != "" && !strings.Contains(strings.ToLower(remedy), "doctor") {
		return remedy
	}
	if verb == "status" && strings.TrimSpace(source) != "" {
		return "run trawl sync " + source + " -v"
	}
	return "retry with -v to see the log location"
}

func searchRowsFromResponse(sources []Source, response *federationv1.SearchResponse) mergedSearchResult {
	byID := make(map[string]Source, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	rows := make([]SearchRow, 0, len(response.GetHits()))
	for _, hit := range response.GetHits() {
		source := byID[hit.GetSourceId()]
		parsed, timeOK := parseSearchTime(hit.GetTimeRfc3339())
		rows = append(rows, SearchRow{
			Source: hit.GetSourceId(), Ref: hit.GetOpenRef(), ShortRef: hit.GetShortRef(), Time: hit.GetTimeRfc3339(), AllDay: hit.GetAllDay(),
			AnchorID: hit.GetAnchorId(), Summary: trawlkit.ResultSummary{Title: hit.GetSummary().GetTitle(), Subtitle: hit.GetSummary().GetSubtitle()}, Archive: searchArchiveContextFromProto(hit.GetArchiveContext()), Evidence: searchEvidenceFromProto(hit.GetEvidence()), Availability: hit.Availability,
			surface: sourceHumanName(source), sourceShortRefs: hasCapability(source, "short_refs"), parsedTime: parsed, timeOK: timeOK,
		})
	}
	total := 0
	for _, source := range response.GetSources() {
		total += int(source.GetTotalMatches())
	}
	more := total - len(rows)
	if more < 0 {
		more = 0
	}
	return mergedSearchResult{Rows: rows, TotalMatches: total, Truncated: response.GetTruncated(), More: more}
}

func searchArchiveContextFromProto(values []*federationv1.ArchiveContext) []trawlkit.ArchiveContext {
	out := make([]trawlkit.ArchiveContext, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, trawlkit.ArchiveContext{Kind: value.GetKind(), Label: value.GetLabel()})
		}
	}
	return out
}

func searchEvidenceFromProto(values []*federationv1.EvidenceFragment) []trawlkit.EvidenceFragment {
	out := make([]trawlkit.EvidenceFragment, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		fragment := trawlkit.EvidenceFragment{Label: value.GetLabel()}
		switch content := value.Content.(type) {
		case *federationv1.EvidenceFragment_Text:
			fragment.Text = &trawlkit.TextEvidence{Runs: searchTextRunsFromProto(content.Text.GetRuns())}
		case *federationv1.EvidenceFragment_Field:
			fragment.Field = &trawlkit.FieldEvidence{Name: content.Field.GetName(), Value: searchTextRunsFromProto(content.Field.GetValue())}
		case *federationv1.EvidenceFragment_Media:
			fragment.Media = &trawlkit.MediaEvidence{ResourceRef: content.Media.GetResourceRef(), Description: searchTextRunsFromProto(content.Media.GetDescription())}
		case *federationv1.EvidenceFragment_Relation:
			fragment.Relation = &trawlkit.RelationEvidence{Relation: content.Relation.GetRelation(), Target: searchTextRunsFromProto(content.Relation.GetTarget())}
		}
		out = append(out, fragment)
	}
	return out
}

func searchTextRunsFromProto(values []*federationv1.TextRun) []trawlkit.TextRun {
	out := make([]trawlkit.TextRun, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, trawlkit.TextRun{Text: value.GetText(), Matched: value.GetMatched()})
		}
	}
	return out
}

func federationOrder(mode searchSortMode) federationv1.SearchOrder {
	if mode == searchSortRelevance {
		return federationv1.SearchOrder_SEARCH_ORDER_RELEVANCE
	}
	return federationv1.SearchOrder_SEARCH_ORDER_RECENCY
}

func wrapWhoSearchSources(sources []federation.SearchSource, identifiers map[string]string) []federation.SearchSource {
	wrapped := append([]federation.SearchSource(nil), sources...)
	for index := range wrapped {
		source := &wrapped[index]
		if source.SkipReason != "" {
			continue
		}
		identifier := strings.TrimSpace(identifiers[source.Manifest.ID])
		if identifier == "" {
			source.SkipReason = "Cannot filter by the resolved person."
			continue
		}
		run := source.Run
		source.Run = func(ctx context.Context, query trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			query.Who = identifier
			return run(ctx, query)
		}
	}
	return wrapped
}

func failureForOpenResponse(response *openv1.OpenResponse) *federationv1.SourceFailure {
	if response == nil {
		return &federationv1.SourceFailure{Message: "Open returned no response."}
	}
	return response.GetFailure()
}

func isNotFoundFailure(failure *federationv1.SourceFailure) bool {
	return failure != nil && failure.GetCode() == federationv1.FailureCode_FAILURE_CODE_NOT_FOUND
}

func isAmbiguousShortRefFailure(failure *federationv1.SourceFailure) bool {
	return failure != nil && failure.GetCode() == federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT && strings.Contains(strings.ToLower(failure.GetMessage()), "ambiguous")
}
