package xapi

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
	"time"
)

const defaultBaseURL = "https://api.x.com"

type Client struct {
	baseURL string
	http    *http.Client
	creds   *Credentials
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
}

type Options struct {
	BaseURL         string
	HTTPClient      *http.Client
	CredentialsPath string
	Now             func() time.Time
	Sleep           func(context.Context, time.Duration) error
}

type RateLimitedError struct {
	ResetAt time.Time
}

func (e *RateLimitedError) Error() string {
	if e.ResetAt.IsZero() {
		return "X API rate limit reached"
	}
	return "X API rate limit reached until " + e.ResetAt.UTC().Format(time.RFC3339)
}

// PaymentRequiredError is X refusing a request because the account's
// credits or billing-cycle spend cap are exhausted on the X side.
type PaymentRequiredError struct{}

func (e *PaymentRequiredError) Error() string {
	return "X refused the request: credits or spend cap exhausted"
}

type AuthError struct {
	message string
}

func (e *AuthError) Error() string { return e.message }

func New(opts Options) (*Client, error) {
	creds, err := LoadCredentials(opts.CredentialsPath)
	if err != nil {
		return nil, err
	}
	if !creds.Ready() {
		return nil, ErrCredentialsIncomplete
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{baseURL: baseURL, http: httpClient, creds: creds, now: now, sleep: sleep}, nil
}

func (c *Client) Me(ctx context.Context) (User, Charge, error) {
	var body meResponse
	if err := c.getJSON(ctx, "/2/users/me", nil, &body); err != nil {
		return User{}, Charge{}, err
	}
	charge := Charge{}
	if body.Data.ID != "" {
		charge.Users = 1
	}
	return body.Data, charge, nil
}

func (c *Client) UserTweets(ctx context.Context, userID string, q PageQuery) (TweetPage, error) {
	return c.timeline(ctx, "/2/users/"+url.PathEscape(userID)+"/tweets", q, true)
}

func (c *Client) Mentions(ctx context.Context, userID string, q PageQuery) (TweetPage, error) {
	// Mentions are other people's posts and bill at the higher rate.
	return c.timeline(ctx, "/2/users/"+url.PathEscape(userID)+"/mentions", q, false)
}

func (c *Client) LikedTweets(ctx context.Context, userID string, q PageQuery) (TweetPage, error) {
	return c.timeline(ctx, "/2/users/"+url.PathEscape(userID)+"/liked_tweets", q, true)
}

func (c *Client) Bookmarks(ctx context.Context, userID string, q PageQuery) (TweetPage, error) {
	return c.timeline(ctx, "/2/users/"+url.PathEscape(userID)+"/bookmarks", q, true)
}

func (c *Client) Tweets(ctx context.Context, ids []string) (TweetPage, error) {
	values := commonTweetValues()
	values.Set("ids", strings.Join(ids, ","))
	var body tweetPageResponse
	if err := c.getJSON(ctx, "/2/tweets", values, &body); err != nil {
		return TweetPage{}, err
	}
	page, err := parseTweetPage(body)
	if err != nil {
		return TweetPage{}, err
	}
	// Metric refresh looks up the owner's own tweets; measured at the
	// owned-post rate on the 2026-07-04 bill.
	page.Charge.OwnedPosts = len(page.Tweets)
	return page, nil
}

type PageQuery struct {
	SinceID         string
	PaginationToken string
	MaxResults      int
}

func (c *Client) timeline(ctx context.Context, path string, q PageQuery, owned bool) (TweetPage, error) {
	values := commonTweetValues()
	maxResults := q.MaxResults
	if maxResults == 0 {
		maxResults = 100
	}
	values.Set("max_results", strconv.Itoa(maxResults))
	if strings.TrimSpace(q.SinceID) != "" {
		values.Set("since_id", strings.TrimSpace(q.SinceID))
	}
	if strings.TrimSpace(q.PaginationToken) != "" {
		values.Set("pagination_token", strings.TrimSpace(q.PaginationToken))
	}
	var body tweetPageResponse
	if err := c.getJSON(ctx, path, values, &body); err != nil {
		return TweetPage{}, err
	}
	page, err := parseTweetPage(body)
	if err != nil {
		return TweetPage{}, err
	}
	if owned {
		page.Charge.OwnedPosts = len(page.Tweets)
	} else {
		page.Charge.OtherPosts = len(page.Tweets)
	}
	page.Charge.Users += len(page.Users)
	return page, nil
}

func commonTweetValues() url.Values {
	values := url.Values{}
	values.Set("tweet.fields", "created_at,public_metrics,in_reply_to_user_id,referenced_tweets,conversation_id,text,author_id")
	values.Set("expansions", "author_id")
	values.Set("user.fields", "username,name")
	return values
}

func parseTweetPage(body tweetPageResponse) (TweetPage, error) {
	page := TweetPage{Users: body.Includes.Users, NextToken: body.Meta.NextToken, NewestID: body.Meta.NewestID}
	for _, raw := range body.Data {
		tweet, err := parseTweet(raw)
		if err != nil {
			return TweetPage{}, err
		}
		page.Tweets = append(page.Tweets, tweet)
	}
	return page, nil
}

func parseTweet(raw json.RawMessage) (Tweet, error) {
	var value rawTweet
	if err := json.Unmarshal(raw, &value); err != nil {
		return Tweet{}, err
	}
	tweet := Tweet{
		ID:             strings.TrimSpace(value.ID),
		Text:           value.Text,
		AuthorID:       strings.TrimSpace(value.AuthorID),
		ConversationID: strings.TrimSpace(value.ConversationID),
		RawJSON:        string(raw),
	}
	if value.PublicMetrics != nil {
		tweet.PublicMetrics = *value.PublicMetrics
		tweet.MetricsReturned = true
	}
	if value.CreatedAt != "" {
		createdAt, err := time.Parse(time.RFC3339, value.CreatedAt)
		if err != nil {
			return Tweet{}, fmt.Errorf("parse tweet created_at: %w", err)
		}
		tweet.CreatedAt = createdAt.UTC()
	}
	for _, ref := range value.ReferencedTweets {
		switch ref.Type {
		case "replied_to":
			tweet.InReplyToID = ref.ID
		case "quoted":
			tweet.QuotedTweetID = ref.ID
		}
	}
	return tweet, nil
}

func (c *Client) getJSON(ctx context.Context, path string, values url.Values, dst any) error {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return err
	}
	if values != nil {
		u.RawQuery = values.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, dst)
}

func (c *Client) doJSON(req *http.Request, dst any) error {
	var lastRateLimit *RateLimitedError
	refreshed := false
	for attempt := 0; attempt <= 2; attempt++ {
		cloned := req.Clone(req.Context())
		cloned.Header.Set("Authorization", "Bearer "+c.creds.AccessToken)
		cloned.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(cloned)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			_ = resp.Body.Close()
			if err := c.refresh(req.Context()); err != nil {
				return err
			}
			refreshed = true
			attempt--
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized {
			_ = resp.Body.Close()
			return &AuthError{message: "X API credentials were rejected"}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resetAt := rateLimitReset(resp.Header.Get("x-rate-limit-reset"))
			_ = resp.Body.Close()
			lastRateLimit = &RateLimitedError{ResetAt: resetAt}
			delay := time.Until(resetAt)
			if !resetAt.IsZero() {
				delay = resetAt.Sub(c.now())
			}
			if delay >= 0 && delay <= time.Minute && attempt < 2 {
				if err := c.sleep(req.Context(), delay); err != nil {
					return err
				}
				continue
			}
			return lastRateLimit
		}
		if resp.StatusCode == http.StatusPaymentRequired {
			_ = resp.Body.Close()
			return &PaymentRequiredError{}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			//nolint:staticcheck // X API is the product name; lowercasing it would make the error less clear.
			return fmt.Errorf("X API request failed with HTTP %d", resp.StatusCode)
		}
		defer func() { _ = resp.Body.Close() }()
		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(dst); err != nil {
			return err
		}
		return nil
	}
	if lastRateLimit != nil {
		return lastRateLimit
	}
	//nolint:staticcheck // X API is the product name; lowercasing it would make the error less clear.
	return errors.New("X API request retry limit reached")
}

func (c *Client) refresh(ctx context.Context) error {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.creds.RefreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/2/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+basicAuth(c.creds.ClientID, c.creds.ClientSecret))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return &AuthError{message: "X API token refresh failed"}
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if err := c.creds.PersistRotatedTokens(body.AccessToken, body.RefreshToken); err != nil {
		return err
	}
	return nil
}

func basicAuth(id, secret string) string {
	return base64.StdEncoding.EncodeToString([]byte(id + ":" + secret))
}

func rateLimitReset(value string) time.Time {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
