package cli

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/proto"
)

const (
	remoteAppStatusPath       = "/app/status"
	remoteAppSearchPath       = "/app/search"
	remoteAppOpenPath         = "/app/open"
	remoteAppPhotoPath        = "/app/photo"
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
	case remoteAppPhotoPath:
		handler.handleRemoteAppPhoto(response, request, scope)
	default:
		writeMCPHTTPError(response, http.StatusNotFound, "no such endpoint; use POST /mcp or GET /app/status, /app/search, /app/open, /app/photo")
	}
}

func (handler *agentMCPHTTPHandler) handleRemoteAppStatus(
	response http.ResponseWriter,
	request *http.Request,
	scope agentSourceScope,
) {
	agentRuntime := handler.service.newAgentRequestRuntime(request.Context(), scope)
	scopedSnapshot := scope.registeredTrawlerManifestSnapshotAllowed(
		handler.service.registeredTrawlerManifestSnapshotAtServerStart,
	)
	statusResponse := agentRuntime.appStatusResponse(agentRuntime.ctx, scopedSnapshot)
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
	if normalizedLimit > mcpMaximumRemoteQueryLimit {
		normalizedLimit = mcpMaximumRemoteQueryLimit
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

	agentRuntime := handler.service.newAgentRequestRuntime(request.Context(), scope)
	trawlers := discoverInstalledTrawlers(agentRuntime.ctx)
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
	searchResponse := agentRuntime.appSearchResponse(
		agentRuntime.ctx,
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

	agentRuntime := handler.service.newAgentRequestRuntime(request.Context(), scope)
	installedTrawlers := discoverInstalledTrawlers(agentRuntime.ctx)
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
	openResponse := agentRuntime.appOpenResponse(
		agentRuntime.ctx,
		route.RegisteredTrawler,
		route.LocalShortReference,
		trawlkit.NewRecordAnchorIdentifier(anchor),
		requestedTrawlLink,
	)
	if err := filterOpenedPersonRecordForAgentScope(
		scope,
		openResponse.GetRecord().GetPersonRecord(),
		installedTrawlerCommandNames(installedTrawlers),
	); err != nil {
		writeMCPHTTPError(response, http.StatusForbidden, "source is not allowed by this access token")
		return
	}
	stripServerLocalPicturePathFromRemoteAppOpenResponse(openResponse)
	openResponse.RequestedTrawlLink = requestedTrawlLink
	writeAppProtobufHTTPResponse(response, openResponse)
}

func stripServerLocalPicturePathFromRemoteAppOpenResponse(openResponse *open.OpenResponse) {
	record := openResponse.GetRecord()
	if record == nil {
		return
	}
	if photoRecord := record.GetOpenedPhotoRecord(); photoRecord != nil {
		photoRecord.CachedPictureFilePath = ""
	}
	detail := record.GetTrawlerSpecificOpenedRecordPresentation().GetDetailPresentation()
	if detail == nil || trawlkit.RegisteredTrawlerIdentityText(record.GetRecordTrawler()) != "photos" {
		return
	}
	for _, field := range detail.GetFieldsInDisplayOrder() {
		if field == nil || field.GetFieldDisplayName() != "Picture" {
			continue
		}
		textValue, isText := field.GetFieldValue().GetTypedValue().(*presentation.TrawlerSpecificCommandPresentationValue_Text)
		if isText && strings.HasPrefix(strings.TrimSpace(textValue.Text), "/") {
			textValue.Text = "Available via remote photo endpoint."
		}
	}
}

func (handler *agentMCPHTTPHandler) handleRemoteAppPhoto(
	response http.ResponseWriter,
	request *http.Request,
	scope agentSourceScope,
) {
	rawLink := strings.TrimSpace(request.URL.Query().Get("link"))
	if rawLink == "" {
		writeMCPHTTPError(response, http.StatusBadRequest, "link is required")
		return
	}
	requestedTrawlLink := trawlkit.NewGloballyRoutableTrawlLink(rawLink)
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(requestedTrawlLink)
	if err != nil {
		applePhotoUUIDArgument := strings.TrimPrefix(rawLink, "photos:")
		if !applePhotoUUIDAcceptedByOpenCommandPattern.MatchString(applePhotoUUIDArgument) {
			writeMCPHTTPError(response, http.StatusBadRequest, "photo link is not valid")
			return
		}
		requestedTrawlLink = nil
		route = trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity("photos"),
			LocalShortReference: trawlkit.NewLocalTrawlerShortReference(strings.ToUpper(applePhotoUUIDArgument)),
		}
	}
	targetTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler)
	if targetTrawlerIdentity != "photos" {
		writeMCPHTTPError(response, http.StatusNotFound, "link is not a photo record")
		return
	}
	if !scope.allowsSource("photos") {
		writeMCPHTTPError(response, http.StatusForbidden, "source is not allowed by this access token")
		return
	}

	agentRuntime := handler.service.newAgentRequestRuntime(request.Context(), scope)
	installedTrawlers := discoverInstalledTrawlers(agentRuntime.ctx)
	selectedTrawler, found := findInstalledTrawler(installedTrawlers, "photos")
	if !found {
		writeMCPHTTPError(response, http.StatusNotFound, "photos source is not available")
		return
	}
	if _, err := trawlersForSharedOperation(
		[]InstalledTrawler{selectedTrawler},
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
		true,
	); err != nil {
		writeMCPHTTPError(response, http.StatusNotFound, "photos source cannot open records")
		return
	}

	openResponse := agentRuntime.canonicalOpen(
		agentRuntime.federationOpenTrawlers(installedTrawlers),
		route.RegisteredTrawler,
		route.LocalShortReference,
		requestedTrawlLink,
	)
	if openResponse.GetFailure() != nil {
		writeMCPHTTPError(response, http.StatusNotFound, "photo record not found")
		return
	}
	openedPhoto := openResponse.GetRecord().GetOpenedPhotoRecord()
	cachedPath := openedPhoto.GetCachedPictureFilePath()
	photoFile, info, err := openVerifiedCachedPhoto(handler.service.stateRoot, cachedPath)
	if err != nil {
		statusCode, errorMessage := remoteAppPhotoUnavailableError(openedPhoto)
		writeMCPHTTPError(response, statusCode, errorMessage)
		return
	}
	defer func() { _ = photoFile.Close() }()

	response.Header().Set("Content-Type", remotePhotoMIMETypeJPEG)
	response.Header().Set("Cache-Control", "private, max-age=3600")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	response.WriteHeader(http.StatusOK)
	_, _ = io.Copy(response, io.LimitReader(photoFile, info.Size()))
}

func remoteAppPhotoUnavailableError(openedPhoto *photo.OpenedPhotoRecord) (int, string) {
	if openedPhoto == nil {
		return http.StatusNotFound, "Photo record was not found."
	}
	fetchResult := openedPhoto.GetPictureFetchResult()
	if fetchResult.GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED {
		message := sanitizeRemotePhotoFailureMessage(fetchResult.GetPictureFetchFailureMessage())
		if message == "" {
			message = "The archive Mac could not fetch this photo."
		}
		if strings.Contains(strings.ToLower(message), "no photo with this uuid") {
			return http.StatusNotFound, message
		}
		return http.StatusServiceUnavailable, message
	}
	if strings.TrimSpace(openedPhoto.GetCachedPictureFilePath()) == "" {
		if fetchResult.GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED {
			return http.StatusServiceUnavailable, "The photo is no longer in the archive Mac's picture cache."
		}
		return http.StatusNotFound, "The photo has not been fetched on the archive Mac yet."
	}
	return http.StatusServiceUnavailable, "The cached photo could not be read on the archive Mac."
}

func sanitizeRemotePhotoFailureMessage(rawMessage string) string {
	trimmed := strings.TrimSpace(rawMessage)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, pathMarker := range []string{"/users/", "/volumes/", "/private/", "/var/", "/tmp/", "/library/"} {
		if strings.Contains(lower, pathMarker) {
			return "The archive Mac could not read the photo file."
		}
	}
	return trimmed
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
