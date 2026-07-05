package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/xapi"
)

const (
	pageSize           = 100
	metricRefreshLimit = 200
)

var xapiBaseURL string
var xapiHTTPClient *http.Client

type syncRunner struct {
	r      *runtime
	st     *store.Store
	client *xapi.Client
	cfg    birdConfig
	now    func() time.Time
	month  string
	totals syncTotals
}

type syncTotals struct {
	Tweets         int   `json:"tweets"`
	Roles          int   `json:"roles"`
	Profiles       int   `json:"profiles"`
	Deficient      int   `json:"deficient_rows"`
	APISpendMicros int64 `json:"api_spend_micros"`
}

type syncEvent struct {
	Type           string      `json:"type"`
	Phase          string      `json:"phase,omitempty"`
	Fetched        int         `json:"fetched,omitempty"`
	StoredTweets   int         `json:"stored_tweets,omitempty"`
	StoredRoles    int         `json:"stored_roles,omitempty"`
	StoredProfiles int         `json:"stored_profiles,omitempty"`
	DeficientRows  int         `json:"deficient_rows,omitempty"`
	Complete       bool        `json:"complete,omitempty"`
	Message        string      `json:"message,omitempty"`
	Totals         *syncTotals `json:"totals,omitempty"`
}

type deficientPageError struct {
	phase string
	total int
	bad   int
}

func (e deficientPageError) Error() string {
	return fmt.Sprintf("%s page had %d deficient rows out of %d", e.phase, e.bad, e.total)
}

type budgetExhaustedError struct{}

func (budgetExhaustedError) Error() string { return "monthly X API budget exhausted" }

func (r *runtime) runSync(args []string) error {
	fs := flag.NewFlagSet("birdcrawl sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		return err
	}
	return r.withStore(func(st *store.Store) error {
		client, err := xapi.New(xapi.Options{BaseURL: xapiBaseURL, HTTPClient: xapiHTTPClient})
		if err != nil {
			return r.syncError(st, err, false)
		}
		now := func() time.Time { return time.Now().UTC() }
		s := &syncRunner{r: r, st: st, client: client, cfg: cfg, now: now, month: now().Format("2006-01")}
		if err := s.run(); err != nil {
			fetched := s.totals.Tweets > 0 || s.totals.Roles > 0 || s.totals.APISpendMicros > 0
			return r.syncError(st, err, fetched)
		}
		return nil
	})
}

func (s *syncRunner) run() error {
	if err := s.resolveIdentity(); err != nil {
		return err
	}
	if err := s.syncBookmarks(); err != nil {
		return err
	}
	if err := s.syncSince("authored"); err != nil {
		return err
	}
	if err := s.syncSince("mentions"); err != nil {
		return err
	}
	if err := s.syncLikes(); err != nil {
		return err
	}
	if err := s.refreshMetrics(); err != nil {
		return err
	}
	now := s.now()
	if err := s.st.CommitLivePage(s.r.ctx, store.LivePage{SyncedAt: now, States: []store.SyncStateUpdate{
		{Kind: "live_sync", Cursor: "", LastResult: "ok", LastSyncAt: now},
		{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now},
	}}); err != nil {
		return err
	}
	return s.print(syncEvent{Type: "sync_complete", Complete: true, Totals: &s.totals, Message: "live X API sync complete"})
}

func (s *syncRunner) resolveIdentity() error {
	if s.cfg.UserID != "" && s.cfg.Handle != "" {
		return nil
	}
	if err := s.beforeRequest(xapi.PriceUserMicros); err != nil {
		return err
	}
	user, charge, err := s.client.Me(s.r.ctx)
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.st.AddSpend(s.r.ctx, s.month, charge.Micros(), now); err != nil {
		return err
	}
	s.totals.APISpendMicros += charge.Micros()
	if err := s.cfg.SaveIdentity(user.ID, user.Username); err != nil {
		return err
	}
	if s.cfg.UserID == "" {
		//nolint:staticcheck // X API is the product name; lowercasing it would make the error less clear.
		return errors.New("X API /2/users/me did not return a user id")
	}
	return s.print(syncEvent{Type: "sync_progress", Phase: "identity", StoredProfiles: 1, Message: "resolved X user identity"})
}

func (s *syncRunner) syncSince(phase string) error {
	cursorKind := "cursor:" + phase
	pageKind := "page:" + phase
	passKind := "pass_newest:" + phase
	cursor, err := s.st.SyncState(s.r.ctx, cursorKind)
	if err != nil {
		return err
	}
	pageState, err := s.st.SyncState(s.r.ctx, pageKind)
	if err != nil {
		return err
	}
	passState, err := s.st.SyncState(s.r.ctx, passKind)
	if err != nil {
		return err
	}
	token := pageState.Cursor
	passNewest := passState.Cursor
	postPrice := xapi.PriceOwnedPostMicros
	if phase == "mentions" {
		postPrice = xapi.PriceOtherPostMicros
	}
	for {
		if err := s.beforeRequest(pageSize * postPrice); err != nil {
			return err
		}
		page, err := s.fetchSincePage(phase, cursor.Cursor, token)
		if err != nil {
			return err
		}
		if passNewest == "" {
			passNewest = page.NewestID
		}
		batch, err := s.convertPage(phase, page, roleForPhase(phase), s.now())
		if err != nil {
			return err
		}
		now := s.now()
		complete := page.NextToken == ""
		states := []store.SyncStateUpdate{
			{Kind: pageKind, Cursor: page.NextToken, LastResult: "partial", LastSyncAt: now},
			{Kind: passKind, Cursor: passNewest, LastResult: "running", LastSyncAt: now},
			{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now},
		}
		if complete {
			states = []store.SyncStateUpdate{
				{Kind: cursorKind, Cursor: firstNonEmpty(passNewest, cursor.Cursor), LastResult: "ok", LastSyncAt: now},
				{Kind: pageKind, Cursor: "", LastResult: "ok", LastSyncAt: now},
				{Kind: passKind, Cursor: "", LastResult: "ok", LastSyncAt: now},
				{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now},
			}
		}
		if err := s.commitBatch(batch, page.Charge, states, now); err != nil {
			return err
		}
		if err := s.printBatch(phase, batch, page, complete); err != nil {
			return err
		}
		if complete {
			return nil
		}
		token = page.NextToken
	}
}

func (s *syncRunner) fetchSincePage(phase, sinceID, token string) (xapi.TweetPage, error) {
	query := xapi.PageQuery{SinceID: sinceID, PaginationToken: token, MaxResults: pageSize}
	if phase == "authored" {
		return s.client.UserTweets(s.r.ctx, s.cfg.UserID, query)
	}
	return s.client.Mentions(s.r.ctx, s.cfg.UserID, query)
}

func (s *syncRunner) syncLikes() error {
	pageState, err := s.st.SyncState(s.r.ctx, "page:likes")
	if err != nil {
		return err
	}
	token := pageState.Cursor
	for {
		if err := s.beforeRequest(pageSize * xapi.PriceOwnedPostMicros); err != nil {
			return err
		}
		page, err := s.client.LikedTweets(s.r.ctx, s.cfg.UserID, xapi.PageQuery{PaginationToken: token, MaxResults: pageSize})
		if err != nil {
			return err
		}
		page, hitKnown, err := s.trimKnownLikes(page)
		if err != nil {
			return err
		}
		batch, err := s.convertPage("likes", page, "like", s.now())
		if err != nil {
			return err
		}
		now := s.now()
		complete := hitKnown || page.NextToken == ""
		next := page.NextToken
		result := "partial"
		if complete {
			next = ""
			result = "ok"
		}
		states := []store.SyncStateUpdate{
			{Kind: "page:likes", Cursor: next, LastResult: result, LastSyncAt: now},
			{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now},
		}
		if err := s.commitBatch(batch, page.Charge, states, now); err != nil {
			return err
		}
		if err := s.printBatch("likes", batch, page, complete); err != nil {
			return err
		}
		if complete {
			return nil
		}
		token = next
	}
}

func (s *syncRunner) trimKnownLikes(page xapi.TweetPage) (xapi.TweetPage, bool, error) {
	out := page
	out.Tweets = nil
	for _, tweet := range page.Tweets {
		known, err := s.st.HasRole(s.r.ctx, tweet.ID, "like")
		if err != nil {
			return xapi.TweetPage{}, false, err
		}
		if known {
			out.NextToken = ""
			out.Charge = page.Charge
			return out, true, nil
		}
		out.Tweets = append(out.Tweets, tweet)
	}
	return out, false, nil
}

func (s *syncRunner) refreshMetrics() error {
	ids, err := s.st.StalestAuthored(s.r.ctx, metricRefreshLimit)
	if err != nil {
		return err
	}
	for len(ids) > 0 {
		n := min(len(ids), pageSize)
		chunk := ids[:n]
		ids = ids[n:]
		if err := s.beforeRequest(int64(len(chunk)) * xapi.PriceOwnedPostMicros); err != nil {
			return err
		}
		page, err := s.client.Tweets(s.r.ctx, chunk)
		if err != nil {
			return err
		}
		batch, err := s.convertPage("metric_refresh", page, "", s.now())
		if err != nil {
			return err
		}
		now := s.now()
		states := []store.SyncStateUpdate{{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now}}
		if err := s.commitBatch(batch, page.Charge, states, now); err != nil {
			return err
		}
		if err := s.printBatch("metric_refresh", batch, page, len(ids) == 0); err != nil {
			return err
		}
	}
	return nil
}

func (s *syncRunner) beforeRequest(projectedMicros int64) error {
	spent, err := s.st.SpendMicros(s.r.ctx, s.month)
	if err != nil {
		return err
	}
	if spent+projectedMicros >= s.cfg.MonthlyBudgetMicros {
		return budgetExhaustedError{}
	}
	return nil
}

func (s *syncRunner) commitBatch(batch convertedPage, charge xapi.Charge, states []store.SyncStateUpdate, now time.Time) error {
	spend := charge.Micros()
	err := s.st.CommitLivePage(s.r.ctx, store.LivePage{
		Tweets:      batch.tweets,
		Roles:       batch.roles,
		Profiles:    batch.profiles,
		States:      states,
		SpendMonth:  s.month,
		SpendMicros: spend,
		SyncedAt:    now,
	})
	if err != nil {
		return err
	}
	s.totals.Tweets += len(batch.tweets)
	s.totals.Roles += len(batch.roles)
	s.totals.Profiles += len(batch.profiles)
	s.totals.Deficient += batch.deficient
	s.totals.APISpendMicros += spend
	return nil
}

func (s *syncRunner) printBatch(phase string, batch convertedPage, page xapi.TweetPage, complete bool) error {
	return s.print(syncEvent{
		Type:           "sync_progress",
		Phase:          phase,
		Fetched:        len(page.Tweets) + batch.deficient,
		StoredTweets:   len(batch.tweets),
		StoredRoles:    len(batch.roles),
		StoredProfiles: len(batch.profiles),
		DeficientRows:  batch.deficient,
		Complete:       complete,
	})
}

func (s *syncRunner) print(event syncEvent) error {
	if s.r.json {
		return json.NewEncoder(s.r.stdout).Encode(event)
	}
	if event.Type == "sync_complete" {
		_, err := fmt.Fprintf(s.r.stdout, "sync complete: %d tweets, %d roles, %d profiles, %d deficient rows\n",
			s.totals.Tweets, s.totals.Roles, s.totals.Profiles, s.totals.Deficient)
		return err
	}
	if event.Message != "" {
		_, err := fmt.Fprintln(s.r.stdout, event.Message)
		return err
	}
	line := fmt.Sprintf("%s: stored %d tweets", humanPhase(event.Phase), event.StoredTweets)
	if event.StoredProfiles > 0 {
		line += fmt.Sprintf(" from %d authors", event.StoredProfiles)
	}
	if event.DeficientRows > 0 {
		line += fmt.Sprintf("; %d rows arrived without id or text", event.DeficientRows)
	}
	_, err := fmt.Fprintln(s.r.stdout, line)
	return err
}

func (r *runtime) syncError(st *store.Store, err error, fetched bool) error {
	var rateLimited *xapi.RateLimitedError
	var deficient deficientPageError
	var authErr *xapi.AuthError
	var payment *xapi.PaymentRequiredError
	var budget budgetExhaustedError
	switch {
	case errors.As(err, &rateLimited):
		if fetched {
			r.recordPartialSync(st, "partial: rate limited")
		}
		return r.contractError("rate_limited", "X API rate limit reached", "re-run birdcrawl sync; it resumes from the committed cursor")
	case errors.As(err, &deficient):
		return r.contractError("deficient_input", err.Error(), "check the X API response shape before storing more rows")
	case errors.As(err, &authErr):
		_ = st.SetAuthTokenValid(r.ctx, false, time.Now().UTC())
		return r.contractError("auth_failed", "X API credentials were rejected", "refresh the OAuth credentials in ~/.opentrawl/birdcrawl/credentials.toml")
	case errors.Is(err, xapi.ErrCredentialsMissing):
		return r.contractError("credentials_missing", "X API credentials are missing", "create ~/.opentrawl/birdcrawl/credentials.toml with OAuth user tokens")
	case errors.Is(err, xapi.ErrCredentialsIncomplete):
		return r.contractError("credentials_missing", "X API credentials are incomplete", "add client_id, client_secret, access_token, and refresh_token to ~/.opentrawl/birdcrawl/credentials.toml")
	case errors.Is(err, xapi.ErrCredentialsPermissions):
		return r.contractError("credentials_missing", "X API credentials file has unsafe permissions", "set ~/.opentrawl/birdcrawl/credentials.toml permissions to 0600")
	case errors.As(err, &payment):
		if fetched {
			r.recordPartialSync(st, "partial: X credits exhausted")
		}
		return r.contractError("payment_required", "X refused the request: credits or the billing-cycle spend cap are exhausted on the X side", "buy credits or raise the spend cap in the X developer console (Billing), then re-run birdcrawl sync")
	case errors.As(err, &budget):
		if fetched {
			r.recordPartialSync(st, "partial: budget exhausted")
		}
		return r.contractError("budget_exhausted", "monthly X API budget exhausted", "raise monthly_budget_usd in config or wait for next month")
	default:
		return err
	}
}

// recordPartialSync keeps status honest: a sync that stored pages before
// stopping is neither "never ran" nor "complete". A sync refused before
// fetching anything must NOT be recorded at all — advancing last_sync on a
// zero-fetch run would claim freshness that no data supports.
func (r *runtime) recordPartialSync(st *store.Store, result string) {
	now := time.Now().UTC()
	_ = st.CommitLivePage(r.ctx, store.LivePage{SyncedAt: now, States: []store.SyncStateUpdate{
		{Kind: "live_sync", Cursor: "", LastResult: result, LastSyncAt: now},
	}})
}

func humanPhase(phase string) string {
	switch phase {
	case "metric_refresh":
		return "count refresh"
	case "identity":
		return "account identity"
	default:
		return phase
	}
}

func roleForPhase(phase string) string {
	switch phase {
	case "authored":
		return "authored"
	case "mentions":
		return "mention"
	default:
		return ""
	}
}

func parseSyncTime(value string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	return time.Now().UTC()
}

func (s *syncRunner) nextBookmarkPass(current string) string {
	next := s.now().UTC().Truncate(time.Second)
	if parsed, err := time.Parse(time.RFC3339Nano, current); err == nil && !next.After(parsed) {
		next = parsed.Add(time.Second)
	}
	return next.Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
