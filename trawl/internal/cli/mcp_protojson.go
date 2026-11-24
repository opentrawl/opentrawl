package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var mcpProtoJSONMarshalOptions = protojson.MarshalOptions{
	UseProtoNames:   false,
	EmitUnpopulated: false,
}

type mcpAroundMatchJSON struct {
	When    string `json:"when,omitempty"`
	How     string `json:"how,omitempty"`
	Source  string `json:"source,omitempty"`
	Summary string `json:"summary,omitempty"`
	Link    string `json:"link,omitempty"`
}

type mcpAroundResultJSON struct {
	AnchorSummary string               `json:"anchorSummary,omitempty"`
	AnchorLink    string               `json:"anchorLink,omitempty"`
	WindowStart   string               `json:"windowStart,omitempty"`
	WindowEnd     string               `json:"windowEnd,omitempty"`
	TotalFound    int                  `json:"totalFound"`
	Matches       []mcpAroundMatchJSON `json:"matches"`
}

// executeMCPToolCall runs one read-only MCP tool over the same typed protobuf
// pipeline as the CLI and Mac app, encoding the result as ProtoJSON for both
// content[0].text and MCP structuredContent.
func (service *agentArchiveService) executeMCPToolCall(
	ctx context.Context,
	scope agentSourceScope,
	toolName string,
	arguments mcpToolArguments,
) (mcpToolCallResult, bool) {
	r := service.newAgentRequestRuntime(ctx, scope)
	switch toolName {
	case "trawl_status":
		return mcpToolResultFromOutcome(executeMCPStatusProtoJSON(r, arguments)), true
	case "trawl_search":
		return mcpToolResultFromOutcome(executeMCPSearchProtoJSON(r, arguments)), true
	case "trawl_open":
		return mcpToolResultFromOutcome(executeMCPOpenProtoJSON(r, arguments)), true
	case "trawl_who":
		return mcpToolResultFromOutcome(executeMCPWhoProtoJSON(r, arguments)), true
	case "trawl_around":
		return mcpToolResultFromOutcome(executeMCPAroundJSON(r, arguments)), true
	default:
		return mcpToolCallResult{}, false
	}
}

func mcpToolResultFromOutcome(payload []byte, failed bool, err error) mcpToolCallResult {
	if err != nil {
		return mcpErrorToolCallResult(mcpErrorMessageText(err))
	}
	trimmed := strings.TrimSpace(string(payload))
	return mcpToolCallResult{
		Content:           []mcpToolTextContent{{Type: "text", Text: trimmed}},
		StructuredContent: json.RawMessage(trimmed),
		IsError:           failed,
	}
}

func mcpErrorToolCallResult(message string) mcpToolCallResult {
	encoded, err := json.MarshalIndent(mcpHTTPErrorResponse{Error: message}, "", "  ")
	if err != nil {
		return mcpToolCallResult{
			Content: []mcpToolTextContent{{Type: "text", Text: message}},
			IsError: true,
		}
	}
	return mcpToolCallResult{
		Content:           []mcpToolTextContent{{Type: "text", Text: string(encoded)}},
		StructuredContent: json.RawMessage(encoded),
		IsError:           true,
	}
}

func mcpErrorMessageText(err error) string {
	if failure, worded := render.AsCommandFailure(err); worded {
		return strings.TrimSpace(failure.Error())
	}
	return strings.TrimSpace(err.Error())
}

func marshalMCPProtoJSON(message proto.Message) ([]byte, error) {
	cloned := proto.Clone(message)
	stripInternalIdentifiersFromProtoMessage(cloned.ProtoReflect())
	compact, err := mcpProtoJSONMarshalOptions.Marshal(cloned)
	if err != nil {
		return nil, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return compact, nil
	}
	return pretty.Bytes(), nil
}

// stripInternalIdentifiersFromProtoMessage removes internal database record
// references, anchors, and exact filter handles recursively so MCP ProtoJSON
// exposes only human/agent-safe links and fields.
func stripInternalIdentifiersFromProtoMessage(message protoreflect.Message) {
	if !message.IsValid() {
		return
	}
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() != protoreflect.MessageKind {
			return true
		}
		if isInternalProtoIdentifierMessage(field.Message().FullName()) {
			message.Clear(field)
			return true
		}
		if field.IsList() {
			list := value.List()
			for i := 0; i < list.Len(); i++ {
				stripInternalIdentifiersFromProtoMessage(list.Get(i).Message())
			}
			return true
		}
		if field.IsMap() {
			if field.MapValue().Kind() == protoreflect.MessageKind {
				value.Map().Range(func(_ protoreflect.MapKey, mapValue protoreflect.Value) bool {
					stripInternalIdentifiersFromProtoMessage(mapValue.Message())
					return true
				})
			}
			return true
		}
		stripInternalIdentifiersFromProtoMessage(value.Message())
		return true
	})
}

func isInternalProtoIdentifierMessage(fullName protoreflect.FullName) bool {
	switch fullName {
	case "trawl.identity.CanonicalArchiveRecordReference",
		"trawl.identity.RecordAnchorIdentifier",
		"trawl.person.ExactPersonFilterIdentifier":
		return true
	default:
		return false
	}
}

func executeMCPStatusProtoJSON(r *Runtime, arguments mcpToolArguments) ([]byte, bool, error) {
	installed := discoverInstalledTrawlers(r.ctx)
	selected := installed
	if requested := strings.TrimSpace(arguments.Trawler); requested != "" {
		var err error
		selected, err = r.selectInstalledTrawlers(installed, splitTrawlerCSV(requested))
		if err != nil {
			return nil, true, err
		}
	}
	response := federation.Status(r.ctx, r.federationStatusTrawlers(selected))
	response.RegisteredTrawlerCatalog = nil
	for _, statusResult := range response.GetTrawlerStatusResults() {
		if archiveStatus := statusResult.GetTrawlerStatusResponse().GetTrawlerArchiveStatus(); archiveStatus != nil {
			archiveStatus.ArchiveMonthlyHistory = nil
		}
	}
	encoded, err := marshalMCPProtoJSON(response)
	if err != nil {
		return nil, true, err
	}
	failed := response.GetOutcome() == federationcontract.OperationOutcome_OPERATION_OUTCOME_FAILED &&
		len(response.GetTrawlerStatusResults()) == 0
	return encoded, failed, nil
}

func executeMCPSearchProtoJSON(r *Runtime, arguments mcpToolArguments) ([]byte, bool, error) {
	limit := arguments.Limit
	if limit <= 0 {
		limit = 20
	}
	normalizedLimit, err := normalizeSearchLimit(limit)
	if err != nil {
		return nil, true, err
	}
	installed := discoverInstalledTrawlers(r.ctx)
	query, selectedTrawlers, trawlerScope, err := r.resolveSearchTarget(installed, strings.Fields(arguments.Query), arguments.Trawler)
	if err != nil {
		return nil, true, err
	}
	searchWasExplicitlyScopedToOneTrawler := strings.TrimSpace(trawlerScope) != "" && len(selectedTrawlers) == 1
	selectedTrawlers, err = trawlersForSharedOperation(
		selectedTrawlers,
		federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		strings.TrimSpace(trawlerScope) != "",
	)
	if err != nil {
		return nil, true, err
	}
	whoInput := strings.TrimSpace(arguments.Who)
	if strings.TrimSpace(query) == "" && whoInput == "" && strings.TrimSpace(arguments.After) == "" && strings.TrimSpace(arguments.Before) == "" {
		return nil, true, render.SearchNeedsWordsPersonOrDates()
	}
	if len(selectedTrawlers) == 0 {
		return nil, true, render.NoTrawlersAreAvailable()
	}

	var whoResolved *personMatchCandidate
	var resolvedPersonMatchFactsFromTrawlers []*person.PersonMatchFactsFromTrawler
	if whoInput != "" {
		candidate, whoErr := r.resolveExactlyOnePerson(installed, whoInput, func(personLink string) []string {
			return []string{"search", "--who", personLink}
		})
		if whoErr != nil {
			return nil, true, whoErr
		}
		whoResolved = &candidate
		resolvedPersonMatchFactsFromTrawlers = candidate.PersonMatchFactsFromTrawlers
	}

	crawlQuery, err := trawlkitSearchQuery(query, searchOptions{
		limit:  normalizedLimit,
		after:  arguments.After,
		before: arguments.Before,
	}, "")
	if err != nil {
		return nil, true, err
	}
	if whoResolved != nil {
		selectedTrawlerDisplayNames := make([]string, 0, len(selectedTrawlers))
		trawlersApplicableToResolvedPerson := make([]InstalledTrawler, 0, len(selectedTrawlers))
		for _, selectedTrawler := range selectedTrawlers {
			selectedTrawlerDisplayNames = append(selectedTrawlerDisplayNames, trawlerHumanName(selectedTrawler))
			facts := personMatchFactsForTrawlerFromFacts(
				resolvedPersonMatchFactsFromTrawlers,
				installedTrawlerIdentityText(selectedTrawler),
			)
			if len(facts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()) > 0 ||
				selectedTrawler.RegisteredTrawlerManifest.GetSearchMatchesResolvedPersonDisplayNames() ||
				selectedTrawler.TrawlerDiscoveryError != nil {
				trawlersApplicableToResolvedPerson = append(trawlersApplicableToResolvedPerson, selectedTrawler)
			}
		}
		if len(trawlersApplicableToResolvedPerson) == 0 {
			return nil, true, render.NothingInTrawlersInvolvesPerson(resolvedWhoName(whoResolved), selectedTrawlerDisplayNames)
		}
		selectedTrawlers = trawlersApplicableToResolvedPerson
	}

	response := r.runUnifiedAndFederatedSearch(
		r.ctx,
		selectedTrawlers,
		crawlQuery,
		normalizedLimit,
		resolvedWhoName(whoResolved),
		resolvedPersonMatchFactsFromTrawlers,
	)
	if searchWasExplicitlyScopedToOneTrawler {
		if userErr := userInputErrorFromFederatedTrawlerOperationFailures(response.GetOperationFailures()); userErr != nil {
			return nil, true, userErr
		}
	}

	sanitized := sanitizeFederatedSearchOperationForMCP(response)
	encoded, err := marshalMCPProtoJSON(sanitized)
	if err != nil {
		return nil, true, err
	}
	failed := sanitized.GetOutcome() == federationcontract.OperationOutcome_OPERATION_OUTCOME_FAILED &&
		len(sanitized.GetSearchMatchesInDisplayOrder()) == 0
	return encoded, failed, nil
}

func sanitizeFederatedSearchOperationForMCP(
	source *federationcontract.FederatedTrawlerSearchOperation,
) *federationcontract.FederatedTrawlerSearchOperation {
	cloned, _ := proto.Clone(source).(*federationcontract.FederatedTrawlerSearchOperation)
	if cloned == nil {
		return &federationcontract.FederatedTrawlerSearchOperation{}
	}
	for _, trawlerResult := range cloned.GetTrawlerSearchResults() {
		trawlerResult.SearchMatchesFromTrawlerInDisplayOrder = nil
		trawlerResult.EmptySearchNextCommands = nil
		if trawlerResult.SearchPersonFilterResolution != nil {
			trawlerResult.SearchPersonFilterResolution.ResolvedExactPersonFilterIdentifiers = nil
		}
	}
	for _, match := range cloned.GetSearchMatchesInDisplayOrder() {
		match.RecordAnchor = nil
		if presentation := match.GetSearchMatchPresentation(); presentation != nil {
			render.PrepareSearchMatchMonetaryAmountForApp(presentation)
			if presentation.GetAppleLibraryPhotoFacts() != nil {
				presentation.PhysicalPlaceNamesSpecificToBroadest = render.SearchMatchPhysicalPlaceNamesForHumanOutput(presentation)
			}
		}
	}
	return cloned
}

func executeMCPOpenProtoJSON(r *Runtime, arguments mcpToolArguments) ([]byte, bool, error) {
	link := strings.TrimSpace(arguments.Link)
	requestedTrawlLink := trawlkit.NewGloballyRoutableTrawlLink(link)
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(requestedTrawlLink)
	if err != nil {
		applePhotoUUIDArgument := strings.TrimPrefix(link, "photos:")
		if !applePhotoUUIDAcceptedByOpenCommandPattern.MatchString(applePhotoUUIDArgument) {
			return nil, true, render.LinkIsNotValid()
		}
		requestedTrawlLink = nil
		route = trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity("photos"),
			LocalShortReference: trawlkit.NewLocalTrawlerShortReference(strings.ToUpper(applePhotoUUIDArgument)),
		}
	}
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	targetTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler)
	if serving, isAgentRequest := agentServingContextFrom(r.ctx); isAgentRequest && !serving.agentSourceScope.allowsSource(targetTrawlerIdentity) {
		return nil, true, render.AgentAccessSourceNotAllowed(targetTrawlerIdentity, installedTrawlerCommandNames(installedTrawlers))
	}
	if selectedTrawler, found := findInstalledTrawler(installedTrawlers, targetTrawlerIdentity); found {
		if _, err := trawlersForSharedOperation([]InstalledTrawler{selectedTrawler}, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, true); err != nil {
			return nil, true, err
		}
	}
	response := r.canonicalOpen(
		r.federationOpenTrawlers(installedTrawlers),
		route.RegisteredTrawler,
		route.LocalShortReference,
		requestedTrawlLink,
	)
	sanitized := sanitizeOpenResponseForMCP(response)
	encoded, err := marshalMCPProtoJSON(sanitized)
	if err != nil {
		return nil, true, err
	}
	failed := sanitized.GetFailure() != nil || sanitized.GetRecord() == nil
	return encoded, failed, nil
}

func sanitizeOpenResponseForMCP(source *open.OpenResponse) *open.OpenResponse {
	cloned, _ := proto.Clone(source).(*open.OpenResponse)
	if cloned == nil {
		return &open.OpenResponse{}
	}
	cloned.RequestedRecordAnchor = nil
	if record := cloned.GetRecord(); record != nil {
		record.CanonicalRecordReference = nil
		if specific := record.GetTrawlerSpecificOpenedRecordPresentation(); specific != nil {
			for _, resolvedLink := range specific.GetResolvedRecordLinks() {
				resolvedLink.CanonicalRecordReference = nil
			}
		}
	}
	return cloned
}

func executeMCPWhoProtoJSON(r *Runtime, arguments mcpToolArguments) ([]byte, bool, error) {
	query := strings.Join(strings.Fields(arguments.Name), " ")
	if query == "" {
		return nil, true, render.WhoNeedsName()
	}
	limit := arguments.Limit
	if limit <= 0 {
		limit = 20
	}
	installed := discoverInstalledTrawlers(r.ctx)
	resolution := resolveWhoThroughContacts(r, installed, query)
	resolution.OperationFailures = append(
		resolution.OperationFailures,
		r.readPersonMessageCountsFromTrawlers(installed, resolution.Candidates)...,
	)
	operation := federatedPersonMatchOperation(resolution, trawlerDisplayNamesByIdentity(installed))
	if len(operation.GetPersonMatchCandidates()) > limit {
		operation.PersonMatchCandidates = operation.GetPersonMatchCandidates()[:limit]
	}
	for _, candidate := range operation.GetPersonMatchCandidates() {
		for _, facts := range candidate.GetPersonMatchFactsFromTrawlers() {
			facts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = nil
		}
	}
	encoded, err := marshalMCPProtoJSON(operation)
	if err != nil {
		return nil, true, err
	}
	failed := len(operation.GetOperationFailures()) > 0 && len(operation.GetPersonMatchCandidates()) == 0
	return encoded, failed, nil
}

func executeMCPAroundJSON(r *Runtime, arguments mcpToolArguments) ([]byte, bool, error) {
	window, err := parseAroundWindowDuration(arguments.Window)
	if err != nil {
		return nil, true, errors.New(err.Error())
	}
	limit := arguments.Limit
	if limit <= 0 {
		limit = 30
	}
	normalizedLimit, err := normalizeSearchLimit(limit)
	if err != nil {
		return nil, true, err
	}
	target := strings.TrimSpace(arguments.Target)
	trip := strings.TrimSpace(arguments.Trip)
	if target == "" && trip == "" {
		return nil, true, errors.New("provide target (a trawl link or YYYY-MM-DD date) or trip")
	}

	installed := discoverInstalledTrawlers(r.ctx)
	if err := r.ensureUnifiedIndexBootstrappedForTrawlers(r.ctx, installed); err != nil {
		return nil, true, err
	}
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return nil, true, err
	}
	indexStore, err := unifiedindex.OpenUnifiedIndexReadOnly(r.ctx, stateRoot, false)
	if err != nil {
		return nil, true, err
	}
	if indexStore == nil {
		return nil, true, errors.New("unified index has not been built yet")
	}
	defer func() { _ = indexStore.Close() }()

	if err := refuseAroundTargetOutsideAgentScope(installed, target); err != nil {
		return nil, true, err
	}
	var trawlerFilter []string
	if requested := strings.TrimSpace(arguments.Trawler); requested != "" {
		selected, selErr := r.selectInstalledTrawlers(installed, splitTrawlerCSV(requested))
		if selErr != nil {
			return nil, true, selErr
		}
		for _, t := range selected {
			trawlerFilter = append(trawlerFilter, installedTrawlerIdentityText(t))
		}
	} else {
		for _, t := range installed {
			trawlerFilter = append(trawlerFilter, installedTrawlerIdentityText(t))
		}
	}

	result, err := unifiedindex.FindAround(r.ctx, indexStore.DB(), unifiedindex.AroundQuery{
		Target:        target,
		TripQuery:     trip,
		Window:        window,
		Limit:         normalizedLimit,
		TrawlerFilter: trawlerFilter,
	})
	if err != nil {
		return nil, true, err
	}

	matches := make([]mcpAroundMatchJSON, 0, len(result.Matches))
	for _, match := range result.Matches {
		matches = append(matches, mcpAroundMatchJSON{
			When:    formatAroundMatchTime(match),
			How:     match.JoinReason,
			Source:  match.TrawlerDisplay,
			Summary: formatAroundMatchSummary(match),
			Link:    match.Link,
		})
	}
	payload := mcpAroundResultJSON{
		AnchorSummary: result.AnchorSummary,
		AnchorLink:    result.AnchorLink,
		TotalFound:    result.TotalFound,
		Matches:       matches,
	}
	if !result.WindowStart.IsZero() {
		payload.WindowStart = result.WindowStart.Local().Format("2006-01-02T15:04:05-07:00")
	}
	if !result.WindowEnd.IsZero() {
		payload.WindowEnd = result.WindowEnd.Local().Format("2006-01-02T15:04:05-07:00")
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, true, err
	}
	return encoded, false, nil
}
