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
	gmailAPIMaximumRetriesPerRequest = 5
	gmailAPIMaximumRetryDelay        = 32 * time.Second
	gmailAPIConcurrentMessageFetches = 5
	// One request slot every 210 ms is about 286 requests a minute. At five
	// quota units for each messages.get, that stays under Gmail's per-user
	// quota and leaves room for list and history requests.
	gmailAPIRequestInterval = 210 * time.Millisecond
)

type GmailAPIClient struct {
	gmailAPIAccessToken func(context.Context) (string, error)
	httpClient          *http.Client
	pacer               gmailAPIRequestPacer
}

func NewGmailAPIClient(gmailAPIAccessToken func(context.Context) (string, error)) *GmailAPIClient {
	return &GmailAPIClient{
		gmailAPIAccessToken: gmailAPIAccessToken,
		httpClient:          &http.Client{Timeout: 5 * time.Minute},
	}
}

// GmailAPIStatusError is a response the client did not retry.
type GmailAPIStatusError struct {
	GmailAPIPath   string
	HTTPStatusCode int
}

func (e GmailAPIStatusError) Error() string {
	return fmt.Sprintf("gmail API %s returned HTTP %d", e.GmailAPIPath, e.HTTPStatusCode)
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
		if internalDate, err := strconv.ParseInt(response.InternalDate, 10, 64); err == nil {
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
	accessToken, err := client.gmailAPIAccessToken(ctx)
	if err != nil {
		return err
	}
	requestURL := gmailAPIBaseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		if err := client.pacer.waitForRequestSlot(ctx); err != nil {
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
		if !gmailAPIResponseIsRetryable(response.StatusCode, errorBody) || attempt >= gmailAPIMaximumRetriesPerRequest {
			return GmailAPIStatusError{GmailAPIPath: path, HTTPStatusCode: response.StatusCode}
		}
		if err := sleepBeforeRetry(ctx, gmailAPIRetryDelay(attempt, response.Header.Get("Retry-After"))); err != nil {
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

// gmailAPIResponseIsRetryable retries rate limits and server errors. Gmail
// reports per-user rate limits as HTTP 403 with a rate-limit reason; any
// other 403 is a real refusal.
func gmailAPIResponseIsRetryable(statusCode int, errorBody []byte) bool {
	switch {
	case statusCode == http.StatusTooManyRequests, statusCode >= 500:
		return true
	case statusCode == http.StatusForbidden:
		var gmailAPIError struct {
			Error struct {
				Errors []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		_ = json.Unmarshal(errorBody, &gmailAPIError)
		for _, detail := range gmailAPIError.Error.Errors {
			if detail.Reason == "rateLimitExceeded" || detail.Reason == "userRateLimitExceeded" {
				return true
			}
		}
	}
	return false
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
