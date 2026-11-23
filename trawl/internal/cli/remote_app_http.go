package cli

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"google.golang.org/protobuf/proto"
)

const (
	remoteAppStatusPath       = "/app/status"
	remoteAppSearchPath       = "/app/search"
	remoteAppOpenPath         = "/app/open"
	remoteAppProtobufMimeType = "application/x-protobuf"
)

func (handler *agentMCPHTTPHandler) handleRemoteAppRequest(
	response http.ResponseWriter,
	request *http.Request,
	scope agentSourceScope,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeMCPHTTPError(response, http.StatusMethodNotAllowed, "remote archive endpoints take GET")
		return
	}
	switch request.URL.Path {
	case remoteAppStatusPath:
		handler.handleRemoteAppStatus(response, request, scope)
	case remoteAppSearchPath:
		handler.handleRemoteAppSearch(response, request, scope)
	case remoteAppOpenPath:
		handler.handleRemoteAppOpen(response, request, scope)
	default:
		writeMCPHTTPError(response, http.StatusNotFound, "no such endpoint; use POST /mcp or GET /app/status, /app/search, /app/open")
	}
}

func (handler *agentMCPHTTPHandler) handleRemoteAppStatus(
	response http.ResponseWriter,
	request *http.Request,
	scope agentSourceScope,
) {
	agentRequest := handler.service.newAgentRequestRuntime(request.Context(), scope)
	scopedSnapshot := scope.registeredTrawlerManifestSnapshotAllowed(
		handler.service.registeredTrawlerManifestSnapshotAtServerStart,
	)
	statusResponse := agentRequest.runtime.appStatusResponse(agentRequest.runtime.ctx, scopedSnapshot)
	writeAppProtobufHTTPResponse(response, statusResponse)
}

func (handler *agentMCPHTTPHandler) handleRemoteAppSearch(
	response http.ResponseWriter,
	request *http.Request,
	scope agentSourceScope,
) {
	queryValues := request.URL.Query()
	queryText := strings.TrimSpace(queryValues.Get("q"))
	requestedTrawlerID := strings.TrimSpace(queryValues.Get("trawler"))
	afterText := strings.TrimSpace(queryValues.Get("after"))
	beforeText := strings.TrimSpace(queryValues.Get("before"))

	requestedLimit := defaultMaximumAppSearchMatchCount
	if rawLimit := strings.TrimSpace(queryValues.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeMCPHTTPError(response, http.StatusBadRequest, "limit must be a positive whole number")
			return
		}
		requestedLimit = parsedLimit
	}
	normalizedLimit, err := normalizeSearchLimit(requestedLimit)
	if err != nil {
		writeMCPHTTPError(response, http.StatusBadRequest, err.Error())
		return
	}
	canonicalSearchQuery, err := trawlkitSearchQuery(queryText, searchOptions{
		limit:  normalizedLimit,
		after:  afterText,
		before: beforeText,
	}, "")
	if err != nil {
		writeMCPHTTPError(response, http.StatusBadRequest, err.Error())
		return
	}
	if canonicalSearchQuery.Text == "" && canonicalSearchQuery.After.IsZero() && canonicalSearchQuery.Before.IsZero() {
		writeMCPHTTPError(response, http.StatusBadRequest, "provide q, after, or before")
		return
	}

	agentRequest := handler.service.newAgentRequestRuntime(request.Context(), scope)
	trawlers := discoverInstalledTrawlers(agentRequest.runtime.ctx)
	if requestedTrawlerID != "" {
		if !scope.allowsSource(requestedTrawlerID) {
			writeMCPHTTPError(response, http.StatusForbidden, "source is not allowed by this access token")
			return
		}
		selected, found := findInstalledTrawler(trawlers, requestedTrawlerID)
		if !found {
			writeMCPHTTPError(response, http.StatusNotFound, "requested source was not found")
			return
		}
		trawlers = []InstalledTrawler{selected}
	}
	trawlers, err = trawlersForSharedOperation(
		trawlers,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		requestedTrawlerID != "",
	)
	if err != nil {
		writeMCPHTTPError(response, http.StatusBadRequest, err.Error())
		return
	}
	canonicalSearchQuery.SearchTotalIsLowerBoundWhenResultLimitIsReached = true
	searchResponse := agentRequest.runtime.appSearchResponse(
		agentRequest.runtime.ctx,
		trawlers,
		canonicalSearchQuery,
		normalizedLimit,
	)
	writeAppProtobufHTTPResponse(response, searchResponse)
}

func (handler *agentMCPHTTPHandler) handleRemoteAppOpen(
	response http.ResponseWriter,
	request *http.Request,
	scope agentSourceScope,
) {
	queryValues := request.URL.Query()
	rawLink := strings.TrimSpace(queryValues.Get("link"))
	anchor := queryValues.Get("anchor")
	if rawLink == "" {
		writeMCPHTTPError(response, http.StatusBadRequest, "link is required")
		return
	}
	requestedTrawlLink := trawlkit.NewGloballyRoutableTrawlLink(rawLink)
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(requestedTrawlLink)
	if err != nil {
		writeMCPHTTPError(response, http.StatusBadRequest, "open link is not valid")
		return
	}
	targetTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler)
	if !scope.allowsSource(targetTrawlerIdentity) {
		writeMCPHTTPError(response, http.StatusForbidden, "source is not allowed by this access token")
		return
	}

	agentRequest := handler.service.newAgentRequestRuntime(request.Context(), scope)
	installedTrawlers := discoverInstalledTrawlers(agentRequest.runtime.ctx)
	if selectedTrawler, found := findInstalledTrawler(installedTrawlers, targetTrawlerIdentity); found {
		if _, err := trawlersForSharedOperation(
			[]InstalledTrawler{selectedTrawler},
			federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
			true,
		); err != nil {
			writeMCPHTTPError(response, http.StatusBadRequest, err.Error())
			return
		}
	}
	openResponse := agentRequest.runtime.appOpenResponse(
		agentRequest.runtime.ctx,
		route.RegisteredTrawler,
		route.LocalShortReference,
		trawlkit.NewRecordAnchorIdentifier(anchor),
		requestedTrawlLink,
	)
	openResponse.RequestedTrawlLink = requestedTrawlLink
	writeAppProtobufHTTPResponse(response, openResponse)
}

func (scope agentSourceScope) registeredTrawlerManifestSnapshotAllowed(
	snapshot registeredTrawlerManifestSnapshot,
) registeredTrawlerManifestSnapshot {
	allowedCatalog := make([]*federation.RegisteredTrawlerCatalogEntry, 0, len(snapshot.registeredTrawlerCatalogEntries))
	for _, entry := range snapshot.registeredTrawlerCatalogEntries {
		if entry == nil {
			continue
		}
		identity := trawlkit.RegisteredTrawlerIdentityText(entry.GetRegisteredTrawlerManifest().GetRegisteredTrawler())
		if scope.allowsSource(identity) {
			allowedCatalog = append(allowedCatalog, entry)
		}
	}
	return registeredTrawlerManifestSnapshot{
		installedTrawlers:                         scope.installedTrawlersAllowed(snapshot.installedTrawlers),
		registeredTrawlerCatalogEntries:           allowedCatalog,
		registeredTrawlerCatalogConstructionError: snapshot.registeredTrawlerCatalogConstructionError,
	}
}

func writeAppProtobufHTTPResponse(response http.ResponseWriter, message proto.Message) {
	size := proto.Size(message)
	if size == 0 || size > appFrameLimit {
		writeMCPHTTPError(response, http.StatusInternalServerError, "remote archive response exceeded size limit")
		return
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		writeMCPHTTPError(response, http.StatusInternalServerError, "remote archive response could not be encoded")
		return
	}
	response.Header().Set("Content-Type", remoteAppProtobufMimeType)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(payload)
}
