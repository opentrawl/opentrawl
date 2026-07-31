package twitter

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"github.com/opentrawl/opentrawl/twitter/internal/xapi"
)

const (
	pageSize           = 100
	metricRefreshLimit = 200
)

var xapiBaseURL string
var xapiHTTPClient *http.Client

type updateRunner struct {
	r      *runtime
	st     *store.Store
	client *xapi.Client
	cfg    birdConfig
	now    func() time.Time
	month  string
	totals updateTotals
}

type updateTotals struct {
	Tweets         int   `json:"tweets"`
	Roles          int   `json:"roles"`
	Profiles       int   `json:"profiles"`
	Deficient      int   `json:"deficient_rows"`
	APISpendMicros int64 `json:"api_spend_micros"`
}

type updateEvent struct {
	Type           string        `json:"type"`
	Phase          string        `json:"phase,omitempty"`
	Fetched        int           `json:"fetched,omitempty"`
	StoredTweets   int           `json:"stored_tweets,omitempty"`
	StoredRoles    int           `json:"stored_roles,omitempty"`
	StoredProfiles int           `json:"stored_profiles,omitempty"`
	DeficientRows  int           `json:"deficient_rows,omitempty"`
	Complete       bool          `json:"complete,omitempty"`
	Message        string        `json:"message,omitempty"`
	Totals         *updateTotals `json:"totals,omitempty"`
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

func (r *runtime) runUpdateReport() (*updatecontract.TrawlerArchiveUpdateReport, error) {
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		return nil, err
	}
	var report *updatecontract.TrawlerArchiveUpdateReport
	err = r.withStore(func(st *store.Store) error {
		client, err := xapi.New(xapi.Options{BaseURL: xapiBaseURL, HTTPClient: xapiHTTPClient})
		if err != nil {
			return r.updateError(st, err, false)
		}
		now := func() time.Time { return time.Now().UTC() }
		s := &updateRunner{r: r, st: st, client: client, cfg: cfg, now: now, month: now().Format("2006-01")}
		if err := s.run(); err != nil {
			fetched := s.totals.Tweets > 0 || s.totals.Roles > 0 || s.totals.APISpendMicros > 0
			return r.updateError(st, err, fetched)
		}
		report = updateReport(s.totals)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if report == nil {
		report = &updatecontract.TrawlerArchiveUpdateReport{}
	}
	return report, nil
}

func (s *updateRunner) run() error {
	if err := s.resolveIdentity(); err != nil {
		return err
	}
	if err := s.updateBookmarks(); err != nil {
		return err
	}
	if err := s.updateSince("authored"); err != nil {
		return err
	}
	if err := s.updateSince("mentions"); err != nil {
		return err
	}
	if err := s.updateLikes(); err != nil {
		return err
	}
	if err := s.refreshMetrics(); err != nil {
		return err
	}
	now := s.now()
	if err := s.st.CommitLivePage(s.r.ctx, store.LivePage{UpdatedAt: now, States: []store.UpdateStateUpdate{
		{Kind: "live_update", Cursor: "", LastResult: "ok", LastUpdateAt: now},
		{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastUpdateAt: now},
	}}); err != nil {
		return err
	}
	return s.print(updateEvent{Type: "update_complete", Complete: true, Totals: &s.totals, Message: "live X update complete"})
}

func (s *updateRunner) resolveIdentity() error {
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
	return s.print(updateEvent{Type: "update_progress", Phase: "identity", StoredProfiles: 1, Message: "resolved X user identity"})
}

func (s *updateRunner) updateSince(phase string) error {
	cursorKind := "cursor:" + phase
	pageKind := "page:" + phase
	passKind := "pass_newest:" + phase
	cursor, err := s.st.UpdateState(s.r.ctx, cursorKind)
	if err != nil {
		return err
	}
	pageState, err := s.st.UpdateState(s.r.ctx, pageKind)
	if err != nil {
		return err
	}
	passState, err := s.st.UpdateState(s.r.ctx, passKind)
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
		states := []store.UpdateStateUpdate{
			{Kind: pageKind, Cursor: page.NextToken, LastResult: "partial", LastUpdateAt: now},
			{Kind: passKind, Cursor: passNewest, LastResult: "running", LastUpdateAt: now},
			{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastUpdateAt: now},
		}
		if complete {
			states = []store.UpdateStateUpdate{
				{Kind: cursorKind, Cursor: firstNonEmpty(passNewest, cursor.Cursor), LastResult: "ok", LastUpdateAt: now},
				{Kind: pageKind, Cursor: "", LastResult: "ok", LastUpdateAt: now},
				{Kind: passKind, Cursor: "", LastResult: "ok", LastUpdateAt: now},
				{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastUpdateAt: now},
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

func (s *updateRunner) fetchSincePage(phase, sinceID, token string) (xapi.TweetPage, error) {
	query := xapi.PageQuery{SinceID: sinceID, PaginationToken: token, MaxResults: pageSize}
	if phase == "authored" {
		return s.client.UserTweets(s.r.ctx, s.cfg.UserID, query)
	}
	return s.client.Mentions(s.r.ctx, s.cfg.UserID, query)
}

func (s *updateRunner) updateLikes() error {
	pageState, err := s.st.UpdateState(s.r.ctx, "page:likes")
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
		states := []store.UpdateStateUpdate{
			{Kind: "page:likes", Cursor: next, LastResult: result, LastUpdateAt: now},
			{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastUpdateAt: now},
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

func (s *updateRunner) trimKnownLikes(page xapi.TweetPage) (xapi.TweetPage, bool, error) {
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

func (s *updateRunner) refreshMetrics() error {
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
		states := []store.UpdateStateUpdate{{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastUpdateAt: now}}
		if err := s.commitBatch(batch, page.Charge, states, now); err != nil {
			return err
		}
		if err := s.printBatch("metric_refresh", batch, page, len(ids) == 0); err != nil {
			return err
		}
	}
	return nil
}

func (s *updateRunner) beforeRequest(projectedMicros int64) error {
	spent, err := s.st.SpendMicros(s.r.ctx, s.month)
	if err != nil {
		return err
	}
	if spent+projectedMicros >= s.cfg.MonthlyBudgetMicros {
		return budgetExhaustedError{}
	}
	return nil
}

func (s *updateRunner) commitBatch(batch convertedPage, charge xapi.Charge, states []store.UpdateStateUpdate, now time.Time) error {
	spend := charge.Micros()
	err := s.st.CommitLivePage(s.r.ctx, store.LivePage{
		Tweets:      batch.tweets,
		Roles:       batch.roles,
		Profiles:    batch.profiles,
		States:      states,
		SpendMonth:  s.month,
		SpendMicros: spend,
		UpdatedAt:   now,
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

func (s *updateRunner) printBatch(phase string, batch convertedPage, page xapi.TweetPage, complete bool) error {
	return s.print(updateEvent{
		Type:           "update_progress",
		Phase:          phase,
		Fetched:        len(page.Tweets) + batch.deficient,
		StoredTweets:   len(batch.tweets),
		StoredRoles:    len(batch.roles),
		StoredProfiles: len(batch.profiles),
		DeficientRows:  batch.deficient,
		Complete:       complete,
	})
}

func (s *updateRunner) print(event updateEvent) error {
	if s.r.req.ReportTrawlerCommandProgress == nil {
		return nil
	}
	if event.Type == "update_complete" {
		s.r.req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "update", Done: int64(s.totals.Tweets), Message: event.Message})
		return nil
	}
	message := ""
	if event.Message != "" {
		message = event.Message
	} else {
		message = fmt.Sprintf("%s stored %s tweets", humanPhase(event.Phase), render.FormatInteger(int64(event.StoredTweets)))
		if event.StoredProfiles > 0 {
			message += fmt.Sprintf(" from %s authors", render.FormatInteger(int64(event.StoredProfiles)))
		}
		if event.DeficientRows > 0 {
			message += fmt.Sprintf("; %s rows arrived without id or text", render.FormatInteger(int64(event.DeficientRows)))
		}
	}
	s.r.req.ReportTrawlerCommandProgress(trawlkit.Progress{
		Phase:   event.Phase,
		Done:    int64(event.StoredTweets),
		Total:   int64(event.Fetched),
		Message: message,
	})
	return nil
}

func updateReport(_ updateTotals) *updatecontract.TrawlerArchiveUpdateReport {
	return &updatecontract.TrawlerArchiveUpdateReport{}
}

func (r *runtime) updateError(st *store.Store, err error, fetched bool) error {
	var rateLimited *xapi.RateLimitedError
	var deficient deficientPageError
	var authErr *xapi.AuthError
	var payment *xapi.PaymentRequiredError
	var budget budgetExhaustedError
	switch {
	case errors.As(err, &rateLimited):
		if fetched {
			r.recordPartialUpdate(st, "partial: rate limited")
		}
		return r.contractError("rate_limited", "X API rate limit reached")
	case errors.As(err, &deficient):
		return r.contractError("deficient_input", err.Error())
	case errors.As(err, &authErr):
		_ = st.SetAuthTokenValid(r.ctx, false, time.Now().UTC())
		return r.contractError("auth_failed", "X API credentials were rejected")
	case errors.Is(err, xapi.ErrCredentialsMissing):
		return r.contractError("credentials_missing", "X API credentials are missing")
	case errors.Is(err, xapi.ErrCredentialsIncomplete):
		return r.contractError("credentials_missing", "X API credentials are incomplete")
	case errors.Is(err, xapi.ErrCredentialsPermissions):
		return r.contractError("credentials_missing", "X API credentials file has unsafe permissions")
	case errors.As(err, &payment):
		if fetched {
			r.recordPartialUpdate(st, "partial: X credits exhausted")
		}
		return r.contractError("payment_required", "X refused the request: credits or the billing-cycle spend cap are exhausted on the X side")
	case errors.As(err, &budget):
		if fetched {
			r.recordPartialUpdate(st, "partial: budget exhausted")
		}
		return r.contractError("budget_exhausted", "monthly X API budget exhausted")
	default:
		return err
	}
}

// recordPartialUpdate keeps status honest: a update that stored pages before
// stopping is neither "never ran" nor "complete". A update refused before
// fetching anything must NOT be recorded at all — advancing last_update on a
// zero-fetch run would claim freshness that no data supports.
func (r *runtime) recordPartialUpdate(st *store.Store, result string) {
	now := time.Now().UTC()
	_ = st.CommitLivePage(r.ctx, store.LivePage{UpdatedAt: now, States: []store.UpdateStateUpdate{
		{Kind: "live_update", Cursor: "", LastResult: result, LastUpdateAt: now},
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

func parseUpdateTime(value string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	return time.Now().UTC()
}

func (s *updateRunner) nextBookmarkPass(current string) string {
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
