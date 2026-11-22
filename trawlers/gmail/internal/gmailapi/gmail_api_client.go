package gmailapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	gmailAPIBaseURL                  = "https://gmail.googleapis.com/gmail/v1/users/me"
	gmailAPIMaximumListPageSize      = 500
	gmailAPIMaximumResponseBytes     = 128 << 20
	gmailAPIMaximumErrorBodyBytes    = 64 << 10
	gmailAPIMaximumRetriesPerRequest = 8
	gmailAPIMaximumRetryDelay        = 64 * time.Second
	gmailAPIConcurrentMessageFetches = 2
	// One request slot every 250 ms is 240 requests a minute (1,200 quota units/min
	// for messages.get), with low concurrency so raw-message downloads do not trip
	// Gmail's rolling per-user burst or bandwidth limits.
	gmailAPIRequestInterval = 250 * time.Millisecond
)

// GmailAPIAccessTokenSource supplies bearer tokens for Gmail REST API calls
// and lets the client drop a cached token that Gmail rejected with HTTP 401.
type GmailAPIAccessTokenSource interface {
	GmailAPIAccessToken(ctx context.Context) (string, error)
	InvalidateRejectedGmailAPIAccessToken(rejectedAccessToken string) bool
}

// SingleUseGmailAPIAccessToken wraps one freshly issued access token that
// cannot be refreshed on HTTP 401.
type SingleUseGmailAPIAccessToken string

func (token SingleUseGmailAPIAccessToken) GmailAPIAccessToken(context.Context) (string, error) {
	return string(token), nil
}

func (SingleUseGmailAPIAccessToken) InvalidateRejectedGmailAPIAccessToken(string) bool {
	return false
}

type GmailAPIClient struct {
	accessTokens GmailAPIAccessTokenSource
	httpClient   *http.Client
	pacer        gmailAPIRequestPacer
}

func NewGmailAPIClient(accessTokens GmailAPIAccessTokenSource) *GmailAPIClient {
	return &GmailAPIClient{
		accessTokens: accessTokens,
		httpClient:   &http.Client{Timeout: 5 * time.Minute},
	}
}

// GmailAPIStatusError is a response the client did not retry.
type GmailAPIStatusError struct {
	GmailAPIPath      string
	HTTPStatusCode    int
	GmailAPIErrorCode string
	GmailAPIErrorText string
}

func (e GmailAPIStatusError) Error() string {
	switch {
	case e.GmailAPIErrorCode != "" && e.GmailAPIErrorText != "":
		return fmt.Sprintf("gmail API %s returned HTTP %d (%s: %s)", e.GmailAPIPath, e.HTTPStatusCode, e.GmailAPIErrorCode, e.GmailAPIErrorText)
	case e.GmailAPIErrorCode != "":
		return fmt.Sprintf("gmail API %s returned HTTP %d (%s)", e.GmailAPIPath, e.HTTPStatusCode, e.GmailAPIErrorCode)
	case e.GmailAPIErrorText != "":
		return fmt.Sprintf("gmail API %s returned HTTP %d (%s)", e.GmailAPIPath, e.HTTPStatusCode, e.GmailAPIErrorText)
	default:
		return fmt.Sprintf("gmail API %s returned HTTP %d", e.GmailAPIPath, e.HTTPStatusCode)
	}
}

func (client *GmailAPIClient) Profile(ctx context.Context) (GmailProfile, error) {
	var response struct {
		EmailAddress string `json:"emailAddress"`
		HistoryID    string `json:"historyId"`
	}
	if err := client.getJSON(ctx, "/profile", nil, &response); err != nil {
		return GmailProfile{}, err
	}
	if response.EmailAddress == "" || response.HistoryID == "" {
		return GmailProfile{}, errors.New("gmail profile has no email address or history identifier")
	}
	return GmailProfile{
		GoogleAccountEmailAddress:     response.EmailAddress,
		CurrentGmailHistoryIdentifier: GmailHistoryIdentifier(response.HistoryID),
	}, nil
}

func (client *GmailAPIClient) ListGmailLabels(ctx context.Context) ([]GmailLabel, error) {
	var response struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"labels"`
	}
	if err := client.getJSON(ctx, "/labels", nil, &response); err != nil {
		return nil, err
	}
	labels := make([]GmailLabel, 0, len(response.Labels))
	for _, label := range response.Labels {
		labels = append(labels, GmailLabel{GmailLabelIdentifier: GmailLabelIdentifier(label.ID), GmailLabelName: label.Name, GmailLabelType: label.Type})
	}
	return labels, nil
}

// ListGmailMessageIdentifiers lists every message outside Spam and Trash,
// or only those with labelIdentifier when it is not empty.
func (client *GmailAPIClient) ListGmailMessageIdentifiers(ctx context.Context, labelIdentifier GmailLabelIdentifier) ([]GmailMessageIdentifier, error) {
	var identifiers []GmailMessageIdentifier
	pageToken := ""
	for {
		query := url.Values{"maxResults": {strconv.Itoa(gmailAPIMaximumListPageSize)}, "includeSpamTrash": {"false"}}
		if labelIdentifier != "" {
			query.Set("labelIds", string(labelIdentifier))
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var response struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := client.getJSON(ctx, "/messages", query, &response); err != nil {
			return nil, err
		}
		for _, message := range response.Messages {
			identifiers = append(identifiers, GmailMessageIdentifier(message.ID))
		}
		if response.NextPageToken == "" {
			return identifiers, nil
		}
		pageToken = response.NextPageToken
	}
}

// FetchRawGmailMessages returns the messages in the order asked. A message
// that no longer exists, or is now in Spam or Trash, is returned as excluded.
func (client *GmailAPIClient) FetchRawGmailMessages(ctx context.Context, identifiers []GmailMessageIdentifier) ([]FetchedRawGmailMessage, []GmailMessageIdentifier, error) {
	return fetchGmailMessagesConcurrently(ctx, identifiers, func(ctx context.Context, identifier GmailMessageIdentifier) (FetchedRawGmailMessage, bool, error) {
		var response struct {
			ID           string   `json:"id"`
			ThreadID     string   `json:"threadId"`
			HistoryID    string   `json:"historyId"`
			InternalDate string   `json:"internalDate"`
			LabelIDs     []string `json:"labelIds"`
			Raw          string   `json:"raw"`
		}
		found, err := client.getMessageJSON(ctx, identifier, "raw", &response)
		if err != nil || !found {
			return FetchedRawGmailMessage{}, !found, err
		}
		labels := gmailLabelIdentifiers(response.LabelIDs)
		if GmailLabelsExcludeMessageFromArchive(labels) {
			return FetchedRawGmailMessage{}, true, nil
		}
		rawMessage, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(response.Raw, "="))
		if err != nil {
			return FetchedRawGmailMessage{}, false, fmt.Errorf("decode raw Gmail message %s: %w", identifier, err)
		}
		fetched := FetchedRawGmailMessage{
			GmailMessageIdentifier: identifier,
			GmailThreadIdentifier:  GmailThreadIdentifier(response.ThreadID),
			GmailHistoryIdentifier: GmailHistoryIdentifier(response.HistoryID),
			GmailLabelIdentifiers:  labels,
			RawRFC5322EmailMessage: rawMessage,
		}
		if internalDate, err := strconv.ParseInt(response.InternalDate, 10, 64); err == nil && internalDate > 0 {
			fetched.GmailInternalDateMilliseconds = internalDate
			fetched.GmailInternalDateWasReported = true
		}
		return fetched, false, nil
	})
}

// FetchGmailMessageLabelStates reads only the labels of messages the archive
// already holds; their raw bytes never change.
func (client *GmailAPIClient) FetchGmailMessageLabelStates(ctx context.Context, identifiers []GmailMessageIdentifier) ([]GmailMessageLabelState, []GmailMessageIdentifier, error) {
	return fetchGmailMessagesConcurrently(ctx, identifiers, func(ctx context.Context, identifier GmailMessageIdentifier) (GmailMessageLabelState, bool, error) {
		var response struct {
			LabelIDs []string `json:"labelIds"`
		}
		found, err := client.getMessageJSON(ctx, identifier, "minimal", &response)
		if err != nil || !found {
			return GmailMessageLabelState{}, !found, err
		}
		labels := gmailLabelIdentifiers(response.LabelIDs)
		if GmailLabelsExcludeMessageFromArchive(labels) {
			return GmailMessageLabelState{}, true, nil
		}
		return GmailMessageLabelState{GmailMessageIdentifier: identifier, GmailLabelIdentifiers: labels}, false, nil
	})
}

// ListGmailMailboxChanges reads the history since cursor. It returns
// ErrGmailHistoryCursorExpired when Google no longer keeps that history.
func (client *GmailAPIClient) ListGmailMailboxChanges(ctx context.Context, cursor GmailHistoryIdentifier) (GmailMailboxChanges, error) {
	type historyMessage struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	lastChangeDeletes := map[GmailMessageIdentifier]bool{}
	var identifiersInFirstSeenOrder []GmailMessageIdentifier
	record := func(identifier string, deletes bool) {
		messageIdentifier := GmailMessageIdentifier(identifier)
		if messageIdentifier == "" {
			return
		}
		if _, seen := lastChangeDeletes[messageIdentifier]; !seen {
			identifiersInFirstSeenOrder = append(identifiersInFirstSeenOrder, messageIdentifier)
		}
		lastChangeDeletes[messageIdentifier] = deletes
	}
	changes := GmailMailboxChanges{}
	pageToken := ""
	for {
		query := url.Values{"startHistoryId": {string(cursor)}, "maxResults": {strconv.Itoa(gmailAPIMaximumListPageSize)}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var response struct {
			History []struct {
				MessagesAdded   []historyMessage `json:"messagesAdded"`
				MessagesDeleted []historyMessage `json:"messagesDeleted"`
				LabelsAdded     []historyMessage `json:"labelsAdded"`
				LabelsRemoved   []historyMessage `json:"labelsRemoved"`
			} `json:"history"`
			NextPageToken string `json:"nextPageToken"`
			HistoryID     string `json:"historyId"`
		}
		err := client.getJSON(ctx, "/history", query, &response)
		var statusError GmailAPIStatusError
		if errors.As(err, &statusError) && statusError.HTTPStatusCode == http.StatusNotFound {
			return GmailMailboxChanges{}, ErrGmailHistoryCursorExpired
		}
		if err != nil {
			return GmailMailboxChanges{}, err
		}
		for _, historyRecord := range response.History {
			for _, added := range historyRecord.MessagesAdded {
				record(added.Message.ID, false)
			}
			for _, labelled := range historyRecord.LabelsAdded {
				record(labelled.Message.ID, false)
			}
			for _, unlabelled := range historyRecord.LabelsRemoved {
				record(unlabelled.Message.ID, false)
			}
			for _, deleted := range historyRecord.MessagesDeleted {
				record(deleted.Message.ID, true)
			}
		}
		if response.HistoryID != "" {
			changes.NextGmailHistoryCursor = GmailHistoryIdentifier(response.HistoryID)
		}
		if response.NextPageToken == "" {
			break
		}
		pageToken = response.NextPageToken
	}
	if changes.NextGmailHistoryCursor == "" {
		return GmailMailboxChanges{}, errors.New("gmail history returned no history identifier")
	}
	for _, identifier := range identifiersInFirstSeenOrder {
		if lastChangeDeletes[identifier] {
			changes.DeletedGmailMessageIdentifiers = append(changes.DeletedGmailMessageIdentifiers, identifier)
		} else {
			changes.ChangedGmailMessageIdentifiers = append(changes.ChangedGmailMessageIdentifiers, identifier)
		}
	}
	return changes, nil
}

// getMessageJSON reports found=false for HTTP 404: the message is gone.
func (client *GmailAPIClient) getMessageJSON(ctx context.Context, identifier GmailMessageIdentifier, format string, destination any) (bool, error) {
	err := client.getJSON(ctx, "/messages/"+url.PathEscape(string(identifier)), url.Values{"format": {format}}, destination)
	var statusError GmailAPIStatusError
	if errors.As(err, &statusError) && statusError.HTTPStatusCode == http.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}

func (client *GmailAPIClient) getJSON(ctx context.Context, path string, query url.Values, destination any) error {
	requestURL := gmailAPIBaseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	retriedRejectedAccessToken := false
	for attempt := 0; ; attempt++ {
		if err := client.pacer.waitForRequestSlot(ctx); err != nil {
			return err
		}
		accessToken, err := client.accessTokens.GmailAPIAccessToken(ctx)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+accessToken)
		request.Header.Set("Accept", "application/json")
		response, err := client.httpClient.Do(request)
		if err != nil {
			if ctx.Err() != nil || attempt >= gmailAPIMaximumRetriesPerRequest {
				return fmt.Errorf("gmail API %s: %w", path, err)
			}
			if err := sleepBeforeRetry(ctx, gmailAPIRetryDelay(attempt, "")); err != nil {
				return err
			}
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return decodeGmailAPIResponse(response, path, destination)
		}
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, gmailAPIMaximumErrorBodyBytes))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized && !retriedRejectedAccessToken && client.accessTokens.InvalidateRejectedGmailAPIAccessToken(accessToken) {
			retriedRejectedAccessToken = true
			continue
		}
		errorReason, errorMessage := gmailAPIErrorDetails(errorBody)
		retryable, isRateLimit := gmailAPIResponseRetryClassification(response.StatusCode, errorReason)
		if !retryable || attempt >= gmailAPIMaximumRetriesPerRequest {
			return GmailAPIStatusError{
				GmailAPIPath:      path,
				HTTPStatusCode:    response.StatusCode,
				GmailAPIErrorCode: errorReason,
				GmailAPIErrorText: errorMessage,
			}
		}
		delay := gmailAPIRetryDelay(attempt, response.Header.Get("Retry-After"))
		if isRateLimit {
			client.pacer.pauseAllWorkersFor(delay)
		}
		if err := sleepBeforeRetry(ctx, delay); err != nil {
			return err
		}
	}
}

func decodeGmailAPIResponse(response *http.Response, path string, destination any) error {
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, gmailAPIMaximumResponseBytes+1))
	if err != nil {
		return fmt.Errorf("gmail API %s: %w", path, err)
	}
	if len(body) > gmailAPIMaximumResponseBytes {
		return fmt.Errorf("gmail API %s response is larger than %d bytes", path, gmailAPIMaximumResponseBytes)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("gmail API %s: decode response: %w", path, err)
	}
	return nil
}

func gmailAPIErrorDetails(errorBody []byte) (reason string, message string) {
	var gmailAPIError struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errorBody, &gmailAPIError); err != nil {
		return "", ""
	}
	for _, detail := range gmailAPIError.Error.Errors {
		if r := strings.TrimSpace(detail.Reason); r != "" {
			reason = r
			break
		}
	}
	message = strings.Join(strings.Fields(gmailAPIError.Error.Message), " ")
	if len(message) > 200 {
		message = message[:200]
	}
	return reason, message
}

// gmailAPIResponseRetryClassification classifies retryable responses and
// distinguishes rate limits so all concurrent workers pause together. Gmail
// reports per-user rate limits as HTTP 403 with a rate-limit reason; any
// other 403 is a real refusal.
func gmailAPIResponseRetryClassification(statusCode int, errorReason string) (retryable bool, isRateLimit bool) {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return true, true
	case statusCode >= 500:
		return true, false
	case statusCode == http.StatusForbidden:
		if errorReason == "rateLimitExceeded" || errorReason == "userRateLimitExceeded" {
			return true, true
		}
	}
	return false, false
}

// gmailAPIRetryDelay follows Retry-After when Google sends it, and otherwise
// waits 1, 2, 4, 8 then 16 seconds.
func gmailAPIRetryDelay(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, gmailAPIMaximumRetryDelay)
	}
	if retryTime, err := http.ParseTime(retryAfter); err == nil {
		return min(max(time.Until(retryTime), 0), gmailAPIMaximumRetryDelay)
	}
	return min(time.Second<<min(attempt, 5), gmailAPIMaximumRetryDelay)
}

func sleepBeforeRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// gmailAPIRequestPacer spaces every request of one client, retries
// included, by gmailAPIRequestInterval.
type gmailAPIRequestPacer struct {
	mutex           sync.Mutex
	nextRequestTime time.Time
}

func (pacer *gmailAPIRequestPacer) pauseAllWorkersFor(delay time.Duration) {
	resumeAt := time.Now().Add(delay)
	pacer.mutex.Lock()
	if resumeAt.After(pacer.nextRequestTime) {
		pacer.nextRequestTime = resumeAt
	}
	pacer.mutex.Unlock()
}

func (pacer *gmailAPIRequestPacer) waitForRequestSlot(ctx context.Context) error {
	pacer.mutex.Lock()
	slot := time.Now()
	if pacer.nextRequestTime.After(slot) {
		slot = pacer.nextRequestTime
	}
	pacer.nextRequestTime = slot.Add(gmailAPIRequestInterval)
	pacer.mutex.Unlock()
	return sleepBeforeRetry(ctx, time.Until(slot))
}

func fetchGmailMessagesConcurrently[FetchedValue any](
	ctx context.Context,
	identifiers []GmailMessageIdentifier,
	fetchOne func(context.Context, GmailMessageIdentifier) (FetchedValue, bool, error),
) ([]FetchedValue, []GmailMessageIdentifier, error) {
	type fetchOutcome struct {
		value    FetchedValue
		excluded bool
	}
	outcomes := make([]fetchOutcome, len(identifiers))
	fetchContext, cancelFetches := context.WithCancel(ctx)
	defer cancelFetches()
	var firstErr error
	var firstErrOnce sync.Once
	indexes := make(chan int)
	var workers sync.WaitGroup
	for range min(gmailAPIConcurrentMessageFetches, len(identifiers)) {
		workers.Go(func() {
			for index := range indexes {
				value, excluded, err := fetchOne(fetchContext, identifiers[index])
				if err != nil {
					firstErrOnce.Do(func() {
						firstErr = err
						cancelFetches()
					})
					continue
				}
				outcomes[index] = fetchOutcome{value: value, excluded: excluded}
			}
		})
	}
	for index := range identifiers {
		if fetchContext.Err() != nil {
			break
		}
		indexes <- index
	}
	close(indexes)
	workers.Wait()
	if firstErr != nil {
		return nil, nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	fetched := make([]FetchedValue, 0, len(identifiers))
	var excluded []GmailMessageIdentifier
	for index, outcome := range outcomes {
		if outcome.excluded {
			excluded = append(excluded, identifiers[index])
		} else {
			fetched = append(fetched, outcome.value)
		}
	}
	return fetched, excluded, nil
}

func gmailLabelIdentifiers(labelIDs []string) []GmailLabelIdentifier {
	labels := make([]GmailLabelIdentifier, 0, len(labelIDs))
	for _, labelID := range labelIDs {
		labels = append(labels, GmailLabelIdentifier(labelID))
	}
	return labels
}
