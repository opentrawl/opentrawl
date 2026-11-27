package xweb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultWebOrigin    = "https://x.com"
	defaultWebUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	maxResponseBytes    = 32 * 1024 * 1024
)

// AuthenticatedWebAccount identifies the X account verified for the active web
// session cookies.
type AuthenticatedWebAccount struct {
	UserID      string
	Username    string
	DisplayName string
	RateLimit   RateLimitObservation
}

// ClientOptions configures a read-only X web-session HTTP client.
type ClientOptions struct {
	Origin      string
	HTTPClient  *http.Client
	SessionPath string
	Credentials *WebSessionCredentials
	CachePath   string
	Policy      RateLimitPolicy
	Now         func() time.Time
	Sleep       func(context.Context, time.Duration) error
	OnRateLimit func(RateLimitObservation)
}

// Client is a strictly read-only X web HTTP client authenticated with browser
// session cookies (auth_token and ct0). It enforces per-request pacing,
// remaining-quota safety margins, and automatic GraphQL query ID refresh.
type Client struct {
	origin         string
	httpClient     *http.Client
	creds          *WebSessionCredentials
	cachePath      string
	policy         RateLimitPolicy
	userAgent      string
	clientUUID     string
	clientDeviceID string
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	onRateLimit    func(RateLimitObservation)

	mu             sync.Mutex
	lastRequestAt  time.Time
	metadata       discoveredWebClientMetadata
	metadataLoaded bool
	verifiedUserID string
}

func NewClient(opts ClientOptions) (*Client, error) {
	creds := opts.Credentials
	if creds == nil {
		var err error
		creds, err = LoadWebSession(opts.SessionPath)
		if err != nil {
			return nil, err
		}
	}
	if !creds.Ready() {
		return nil, ErrWebSessionIncomplete
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepWithContext
	}
	origin := strings.TrimRight(strings.TrimSpace(opts.Origin), "/")
	if origin == "" {
		origin = defaultWebOrigin
	}
	cachePath := strings.TrimSpace(opts.CachePath)
	if cachePath == "" && origin == defaultWebOrigin {
		cachePath = defaultDiscoveryCachePath(creds.Path)
	}
	return &Client{
		origin:         origin,
		httpClient:     httpClient,
		creds:          creds,
		cachePath:      cachePath,
		policy:         opts.Policy.withDefaults(),
		userAgent:      defaultWebUserAgent,
		clientUUID:     randomHex(16),
		clientDeviceID: randomHex(16),
		now:            now,
		sleep:          sleep,
		onRateLimit:    opts.OnRateLimit,
	}, nil
}

// VerifyAccount confirms the session cookies against X's viewerQuery endpoint
// and returns the authenticated user's stable numeric ID and handle.
func (c *Client) VerifyAccount(ctx context.Context) (AuthenticatedWebAccount, error) {
	account, err := c.verifyAccountAttempt(ctx, false)
	if err == nil {
		return account, nil
	}
	var authErr *WebSessionAuthError
	var rlErr *WebRateLimitedError
	if errors.As(err, &authErr) || errors.As(err, &rlErr) {
		return AuthenticatedWebAccount{}, err
	}
	return c.verifyAccountAttempt(ctx, true)
}

func (c *Client) verifyAccountAttempt(ctx context.Context, forceRefresh bool) (AuthenticatedWebAccount, error) {
	meta, _ := c.ensureWebMetadata(ctx, forceRefresh)
	if strings.TrimSpace(meta.ViewerQueryID) == "" {
		return AuthenticatedWebAccount{}, errors.New("web client bundles did not include viewerQuery")
	}
	varsMap := make(map[string]bool, len(meta.ViewerProvidedVariables))
	for _, name := range meta.ViewerProvidedVariables {
		varsMap[name] = true
	}
	varsJSON, err := json.Marshal(varsMap)
	if err != nil {
		return AuthenticatedWebAccount{}, err
	}
	params := url.Values{}
	params.Set("variables", string(varsJSON))
	path := fmt.Sprintf("/i/api/graphql/%s/viewerQuery?%s", meta.ViewerQueryID, params.Encode())
	body, obs, statusCode, err := c.doWebRequest(ctx, WebEndpointViewer, http.MethodGet, path, nil, "")
	if err != nil {
		return AuthenticatedWebAccount{}, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return AuthenticatedWebAccount{}, fmt.Errorf("web viewerQuery failed with HTTP %d", statusCode)
	}
	var payload struct {
		Data struct {
			Viewer   *viewerContainer `json:"viewer"`
			ViewerV2 *viewerContainer `json:"viewer_v2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return AuthenticatedWebAccount{}, fmt.Errorf("decode viewerQuery: %w", err)
	}
	container := payload.Data.Viewer
	if container == nil {
		container = payload.Data.ViewerV2
	}
	if container == nil || container.UserResults == nil || container.UserResults.Result == nil {
		return AuthenticatedWebAccount{}, &WebSessionAuthError{Message: "X web session is not signed in"}
	}
	user := unwrapGraphQLUser(container.UserResults.Result)
	if user == nil || !isDigits(strings.TrimSpace(user.RestID)) {
		return AuthenticatedWebAccount{}, &WebSessionAuthError{Message: "X web session did not return a user id"}
	}
	var username, displayName string
	if user.Core != nil {
		username = strings.TrimPrefix(strings.TrimSpace(user.Core.ScreenName), "@")
		displayName = strings.TrimSpace(user.Core.Name)
	}
	if user.Legacy != nil {
		if username == "" {
			username = strings.TrimPrefix(strings.TrimSpace(user.Legacy.ScreenName), "@")
		}
		if displayName == "" {
			displayName = strings.TrimSpace(user.Legacy.Name)
		}
	}
	if username == "" {
		return AuthenticatedWebAccount{}, &WebSessionAuthError{Message: "X web session did not return a username"}
	}
	c.mu.Lock()
	c.verifiedUserID = strings.TrimSpace(user.RestID)
	c.mu.Unlock()
	return AuthenticatedWebAccount{
		UserID:      strings.TrimSpace(user.RestID),
		Username:    username,
		DisplayName: firstNonEmptyString(displayName, username),
		RateLimit:   obs,
	}, nil
}

type viewerContainer struct {
	UserResults *struct {
		Result *rawGraphQLUserResult `json:"result"`
	} `json:"user_results"`
}

func (c *Client) FetchBookmarksPage(ctx context.Context, count int, cursor string) (WebTimelinePage, error) {
	if count <= 0 {
		count = 20
	}
	variables := map[string]any{
		"count":                    count,
		"includePromotedContent":   false,
		"withDownvotePerspective":  false,
		"withReactionsMetadata":    false,
		"withReactionsPerspective": false,
	}
	if strings.TrimSpace(cursor) != "" {
		variables["cursor"] = strings.TrimSpace(cursor)
	}
	return c.fetchGraphQLTimeline(ctx, WebEndpointBookmarks, "Bookmarks", http.MethodGet, variables, bookmarksFeatures(), nil, extractBookmarksInstructions)
}

func (c *Client) FetchLikesPage(ctx context.Context, userID string, count int, cursor string) (WebTimelinePage, error) {
	if count <= 0 {
		count = 20
	}
	variables := map[string]any{
		"userId":                 strings.TrimSpace(userID),
		"count":                  count,
		"includePromotedContent": false,
		"withClientEventToken":   false,
		"withBirdwatchNotes":     false,
		"withVoice":              true,
	}
	if strings.TrimSpace(cursor) != "" {
		variables["cursor"] = strings.TrimSpace(cursor)
	}
	return c.fetchGraphQLTimeline(ctx, WebEndpointLikes, "Likes", http.MethodGet, variables, timelineFeatures(), nil, extractUserTimelineInstructions)
}

func (c *Client) FetchUserTweetsPage(ctx context.Context, userID string, count int, cursor string) (WebTimelinePage, error) {
	if count <= 0 {
		count = 20
	}
	variables := map[string]any{
		"userId":                                 strings.TrimSpace(userID),
		"count":                                  count,
		"includePromotedContent":                 false,
		"withQuickPromoteEligibilityTweetFields": true,
		"withVoice":                              true,
	}
	if strings.TrimSpace(cursor) != "" {
		variables["cursor"] = strings.TrimSpace(cursor)
	}
	fieldToggles := map[string]any{"withArticlePlainText": false}
	return c.fetchGraphQLTimeline(ctx, WebEndpointAuthored, "UserTweets", http.MethodGet, variables, userTweetsFeatures(), fieldToggles, extractUserTimelineInstructions)
}

func (c *Client) FetchMentionsPage(ctx context.Context, handle string, count int, cursor string) (WebTimelinePage, error) {
	if count <= 0 {
		count = 20
	}
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	variables := map[string]any{
		"rawQuery":    "@" + handle,
		"count":       count,
		"querySource": "typed_query",
		"product":     "Latest",
	}
	if strings.TrimSpace(cursor) != "" {
		variables["cursor"] = strings.TrimSpace(cursor)
	}
	return c.fetchGraphQLTimeline(ctx, WebEndpointMentions, "SearchTimeline", http.MethodPost, variables, searchFeatures(), nil, extractSearchTimelineInstructions)
}

func (c *Client) FetchDirectMessageInboxInitialState(ctx context.Context, ownerUserID string) (WebDirectMessageInboxPage, error) {
	path := "/i/api/1.1/dm/inbox_initial_state.json?" + dmInboxQueryParams("").Encode()
	body, obs, statusCode, err := c.doWebRequest(ctx, WebEndpointDMs, http.MethodGet, path, nil, c.origin+"/messages")
	if err != nil {
		return WebDirectMessageInboxPage{}, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return WebDirectMessageInboxPage{}, fmt.Errorf("web DM inbox failed with HTTP %d", statusCode)
	}
	page, err := parseDMInboxPayload(body, ownerUserID)
	if err != nil {
		return WebDirectMessageInboxPage{}, fmt.Errorf("decode DM inbox: %w", err)
	}
	page.RateLimit = obs
	return page, nil
}

func (c *Client) FetchDirectMessageInboxTimeline(ctx context.Context, ownerUserID, kind, maxID string) (WebDirectMessageInboxPage, error) {
	if kind != "trusted" && kind != "untrusted" {
		return WebDirectMessageInboxPage{}, fmt.Errorf("invalid DM inbox timeline kind %q", kind)
	}
	path := fmt.Sprintf("/i/api/1.1/dm/inbox_timeline/%s.json?%s", kind, dmInboxQueryParams(maxID).Encode())
	body, obs, statusCode, err := c.doWebRequest(ctx, WebEndpointDMs, http.MethodGet, path, nil, c.origin+"/messages")
	if err != nil {
		return WebDirectMessageInboxPage{}, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return WebDirectMessageInboxPage{}, fmt.Errorf("web DM %s timeline failed with HTTP %d", kind, statusCode)
	}
	page, err := parseDMInboxPayload(body, ownerUserID)
	if err != nil {
		return WebDirectMessageInboxPage{}, fmt.Errorf("decode DM %s timeline: %w", kind, err)
	}
	page.RateLimit = obs
	return page, nil
}

func dmInboxQueryParams(maxID string) url.Values {
	params := url.Values{}
	params.Set("include_groups", "true")
	params.Set("include_inbox_timelines", "true")
	params.Set("dm_users", "true")
	params.Set("skip_status", "1")
	params.Set("filter_low_quality", "false")
	params.Set("nsfw_filtering_enabled", "false")
	params.Set("include_quality", "all")
	params.Set("dm_secret_conversations_enabled", "false")
	params.Set("krs_registration_enabled", "false")
	if strings.TrimSpace(maxID) != "" {
		params.Set("max_id", strings.TrimSpace(maxID))
	}
	return params
}

type instructionExtractor func([]byte) ([]graphqlTimelineInstruction, error)

func (c *Client) fetchGraphQLTimeline(
	ctx context.Context,
	endpoint WebEndpointName,
	operation string,
	method string,
	variables map[string]any,
	features map[string]bool,
	fieldToggles map[string]any,
	extract instructionExtractor,
) (WebTimelinePage, error) {
	page, needRefresh, err := c.fetchGraphQLTimelineAttempt(ctx, endpoint, operation, method, variables, features, fieldToggles, extract)
	if err == nil || !needRefresh {
		return page, err
	}
	_, _ = c.ensureWebMetadata(ctx, true)
	page, _, err = c.fetchGraphQLTimelineAttempt(ctx, endpoint, operation, method, variables, features, fieldToggles, extract)
	return page, err
}

func (c *Client) fetchGraphQLTimelineAttempt(
	ctx context.Context,
	endpoint WebEndpointName,
	operation string,
	method string,
	variables map[string]any,
	features map[string]bool,
	fieldToggles map[string]any,
	extract instructionExtractor,
) (WebTimelinePage, bool, error) {
	varsJSON, err := json.Marshal(variables)
	if err != nil {
		return WebTimelinePage{}, false, err
	}
	featuresJSON, err := json.Marshal(features)
	if err != nil {
		return WebTimelinePage{}, false, err
	}
	var togglesJSON []byte
	if len(fieldToggles) > 0 {
		togglesJSON, err = json.Marshal(fieldToggles)
		if err != nil {
			return WebTimelinePage{}, false, err
		}
	}

	candidates := c.queryIDCandidates(ctx, operation)
	hadQueryMismatch := false
	var lastErr error
	for _, queryID := range candidates {
		params := url.Values{}
		params.Set("variables", string(varsJSON))
		var postBody []byte
		if method == http.MethodPost {
			payload := map[string]any{
				"features": features,
				"queryId":  queryID,
			}
			postBody, err = json.Marshal(payload)
			if err != nil {
				return WebTimelinePage{}, false, err
			}
		} else {
			params.Set("features", string(featuresJSON))
			if len(togglesJSON) > 0 {
				params.Set("fieldToggles", string(togglesJSON))
			}
		}
		path := fmt.Sprintf("/i/api/graphql/%s/%s?%s", queryID, operation, params.Encode())
		body, obs, statusCode, reqErr := c.doWebRequest(ctx, endpoint, method, path, postBody, "")
		if reqErr != nil {
			return WebTimelinePage{}, false, reqErr
		}
		if statusCode == http.StatusNotFound || statusCode == http.StatusUnprocessableEntity {
			hadQueryMismatch = true
			lastErr = fmt.Errorf("web %s failed with HTTP %d", operation, statusCode)
			continue
		}
		if statusCode < 200 || statusCode >= 300 {
			return WebTimelinePage{}, false, fmt.Errorf("web %s failed with HTTP %d", operation, statusCode)
		}
		instructions, parseErr := extract(body)
		if parseErr != nil {
			if errors.Is(parseErr, errGraphQLValidationFailed) {
				hadQueryMismatch = true
				lastErr = parseErr
				continue
			}
			return WebTimelinePage{}, false, parseErr
		}
		tweets, profiles, nextCursor := parseTimelineInstructions(instructions)
		return WebTimelinePage{
			Tweets:     tweets,
			Profiles:   profiles,
			NextCursor: nextCursor,
			RateLimit:  obs,
		}, false, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("web %s had no query ids", operation)
	}
	return WebTimelinePage{}, hadQueryMismatch, lastErr
}

func (c *Client) doWebRequest(
	ctx context.Context,
	endpoint WebEndpointName,
	method string,
	path string,
	body []byte,
	referer string,
) ([]byte, RateLimitObservation, int, error) {
	if !strings.HasPrefix(path, "/i/api/") {
		return nil, RateLimitObservation{}, 0, errors.New("refusing non-API X web path")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.waitForRequestPacing(ctx); err != nil {
			return nil, RateLimitObservation{}, 0, err
		}
		var bodyReader io.Reader
		if len(body) > 0 {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bodyReader)
		if err != nil {
			return nil, RateLimitObservation{}, 0, err
		}
		c.setWebHeaders(ctx, req, len(body) > 0, referer)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, RateLimitObservation{}, 0, err
		}
		respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, RateLimitObservation{}, resp.StatusCode, readErr
		}

		now := c.now()
		obs := parseRateLimitHeaders(endpoint, resp.Header, resp.StatusCode, now, c.policy.FallbackCooldown)
		if c.onRateLimit != nil && (obs.HasQuota || obs.RateLimited) {
			c.onRateLimit(obs)
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, obs, resp.StatusCode, &WebSessionAuthError{
				Message: fmt.Sprintf("X web session rejected with HTTP %d", resp.StatusCode),
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			delay := obs.ResetAt.Sub(now)
			if attempt == 0 && delay > 0 && delay <= c.policy.ShortWaitThreshold {
				if err := c.sleep(ctx, delay+time.Second); err != nil {
					return nil, obs, resp.StatusCode, err
				}
				continue
			}
			return nil, obs, resp.StatusCode, &WebRateLimitedError{
				Endpoint:  endpoint,
				ResetAt:   obs.ResetAt,
				Limit:     obs.Limit,
				Remaining: obs.Remaining,
			}
		}
		return respBytes, obs, resp.StatusCode, nil
	}
	return nil, RateLimitObservation{}, http.StatusTooManyRequests, &WebRateLimitedError{
		Endpoint: endpoint,
		ResetAt:  c.now().Add(c.policy.FallbackCooldown),
	}
}

func (c *Client) waitForRequestPacing(ctx context.Context) error {
	c.mu.Lock()
	last := c.lastRequestAt
	c.mu.Unlock()
	if !last.IsZero() {
		targetGap := c.policy.MinRequestGap + jitterDuration(c.policy.MaxRequestJitter)
		elapsed := c.now().Sub(last)
		if remaining := targetGap - elapsed; remaining > 0 {
			if err := c.sleep(ctx, remaining); err != nil {
				return err
			}
		}
	}
	c.mu.Lock()
	c.lastRequestAt = c.now()
	c.mu.Unlock()
	return nil
}

func (c *Client) setWebHeaders(ctx context.Context, req *http.Request, hasJSONBody bool, referer string) {
	if strings.TrimSpace(referer) == "" {
		referer = c.origin + "/"
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Authorization", "Bearer "+c.bearerToken(ctx))
	req.Header.Set("Cookie", c.creds.cookieHeader())
	req.Header.Set("X-Csrf-Token", c.creds.CT0)
	req.Header.Set("X-Twitter-Auth-Type", "OAuth2Session")
	req.Header.Set("X-Twitter-Active-User", "yes")
	req.Header.Set("X-Twitter-Client-Language", "en")
	req.Header.Set("X-Client-Uuid", c.clientUUID)
	req.Header.Set("X-Twitter-Client-Deviceid", c.clientDeviceID)
	req.Header.Set("X-Client-Transaction-Id", randomHex(16))
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Referer", referer)
	if hasJSONBody {
		req.Header.Set("Content-Type", "application/json")
	}
	c.mu.Lock()
	uid := c.verifiedUserID
	c.mu.Unlock()
	if uid != "" {
		req.Header.Set("X-Twitter-Client-User-Id", uid)
	}
}

var errGraphQLValidationFailed = errors.New("graphql validation failed")

type graphqlErrorsEnvelope struct {
	Errors []struct {
		Message    string `json:"message"`
		Extensions struct {
			Code string `json:"code"`
		} `json:"extensions"`
	} `json:"errors"`
}

func checkGraphQLErrors(body []byte, hasInstructions bool) error {
	var env graphqlErrorsEnvelope
	if err := json.Unmarshal(body, &env); err != nil || len(env.Errors) == 0 {
		return nil
	}
	for _, e := range env.Errors {
		if e.Extensions.Code == "GRAPHQL_VALIDATION_FAILED" || strings.Contains(e.Message, "Query: Unspecified") {
			return errGraphQLValidationFailed
		}
	}
	if hasInstructions {
		return nil
	}
	return fmt.Errorf("web GraphQL error: %s", env.Errors[0].Message)
}

func extractBookmarksInstructions(body []byte) ([]graphqlTimelineInstruction, error) {
	var env struct {
		Data struct {
			BookmarkTimelineV2 struct {
				Timeline struct {
					Instructions []graphqlTimelineInstruction `json:"instructions"`
				} `json:"timeline"`
			} `json:"bookmark_timeline_v2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode Bookmarks response: %w", err)
	}
	instructions := env.Data.BookmarkTimelineV2.Timeline.Instructions
	if err := checkGraphQLErrors(body, len(instructions) > 0); err != nil {
		return nil, err
	}
	return instructions, nil
}

func extractUserTimelineInstructions(body []byte) ([]graphqlTimelineInstruction, error) {
	var env struct {
		Data struct {
			User struct {
				Result struct {
					Timeline struct {
						Timeline struct {
							Instructions []graphqlTimelineInstruction `json:"instructions"`
						} `json:"timeline"`
					} `json:"timeline"`
					TimelineV2 struct {
						Timeline struct {
							Instructions []graphqlTimelineInstruction `json:"instructions"`
						} `json:"timeline"`
					} `json:"timeline_v2"`
				} `json:"result"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode user timeline response: %w", err)
	}
	instructions := env.Data.User.Result.Timeline.Timeline.Instructions
	if len(instructions) == 0 {
		instructions = env.Data.User.Result.TimelineV2.Timeline.Instructions
	}
	if err := checkGraphQLErrors(body, len(instructions) > 0); err != nil {
		return nil, err
	}
	return instructions, nil
}

func extractSearchTimelineInstructions(body []byte) ([]graphqlTimelineInstruction, error) {
	var env struct {
		Data struct {
			SearchByRawQuery struct {
				SearchTimeline struct {
					Timeline struct {
						Instructions []graphqlTimelineInstruction `json:"instructions"`
					} `json:"timeline"`
				} `json:"search_timeline"`
			} `json:"search_by_raw_query"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode SearchTimeline response: %w", err)
	}
	instructions := env.Data.SearchByRawQuery.SearchTimeline.Timeline.Instructions
	if err := checkGraphQLErrors(body, len(instructions) > 0); err != nil {
		return nil, err
	}
	return instructions, nil
}

func searchFeatures() map[string]bool {
	return map[string]bool{
		"rweb_video_screen_enabled":                                               true,
		"profile_label_improvements_pcf_label_in_post_enabled":                    true,
		"responsive_web_profile_redirect_enabled":                                 true,
		"rweb_tipjar_consumption_enabled":                                         true,
		"verified_phone_label_enabled":                                            false,
		"creator_subscriptions_tweet_preview_api_enabled":                         true,
		"responsive_web_graphql_timeline_navigation_enabled":                      true,
		"responsive_web_graphql_exclude_directive_enabled":                        true,
		"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
		"premium_content_api_read_enabled":                                        false,
		"communities_web_enable_tweet_community_results_fetch":                    true,
		"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
		"responsive_web_grok_analyze_button_fetch_trends_enabled":                 false,
		"responsive_web_grok_analyze_post_followups_enabled":                      false,
		"responsive_web_grok_annotations_enabled":                                 false,
		"responsive_web_jetfuel_frame":                                            true,
		"post_ctas_fetch_enabled":                                                 true,
		"responsive_web_grok_share_attachment_enabled":                            true,
		"responsive_web_edit_tweet_api_enabled":                                   true,
		"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
		"view_counts_everywhere_api_enabled":                                      true,
		"longform_notetweets_consumption_enabled":                                 true,
		"responsive_web_twitter_article_tweet_consumption_enabled":                true,
		"tweet_awards_web_tipping_enabled":                                        false,
		"responsive_web_grok_show_grok_translated_post":                           false,
		"responsive_web_grok_analysis_button_from_backend":                        true,
		"creator_subscriptions_quote_tweet_preview_enabled":                       false,
		"freedom_of_speech_not_reach_fetch_enabled":                               true,
		"standardized_nudges_misinfo":                                             true,
		"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
		"rweb_video_timestamps_enabled":                                           true,
		"longform_notetweets_rich_text_read_enabled":                              true,
		"longform_notetweets_inline_media_enabled":                                true,
		"responsive_web_grok_image_annotation_enabled":                            true,
		"responsive_web_grok_imagine_annotation_enabled":                          true,
		"responsive_web_grok_community_note_auto_translation_is_enabled":          false,
		"articles_preview_enabled":                                                true,
		"responsive_web_enhance_cards_enabled":                                    false,
	}
}

func timelineFeatures() map[string]bool {
	f := searchFeatures()
	f["blue_business_profile_image_shape_enabled"] = true
	f["responsive_web_text_conversations_enabled"] = false
	f["tweetypie_unmention_optimization_enabled"] = true
	f["vibe_api_enabled"] = true
	f["responsive_web_twitter_blue_verified_badge_is_enabled"] = true
	f["interactive_text_enabled"] = true
	f["longform_notetweets_richtext_consumption_enabled"] = true
	f["responsive_web_media_download_video_enabled"] = false
	return f
}

func bookmarksFeatures() map[string]bool {
	f := timelineFeatures()
	f["graphql_timeline_v2_bookmark_timeline"] = true
	return f
}

func userTweetsFeatures() map[string]bool {
	f := searchFeatures()
	f["rweb_video_screen_enabled"] = false
	f["responsive_web_profile_redirect_enabled"] = false
	f["responsive_web_grok_analyze_post_followups_enabled"] = true
	f["responsive_web_grok_show_grok_translated_post"] = true
	return f
}

func randomHex(numBytes int) string {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", numBytes*2)
	}
	return hex.EncodeToString(buf)
}
