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
	"github.com/opentrawl/opentrawl/twitter/internal/xweb"
	"google.golang.org/protobuf/proto"
)

const (
	webSessionMinSyncInterval        = 15 * time.Minute
	webSessionPageSize               = 20
	webSessionDefaultIncrementalMax  = 5
	webSessionDefaultMentionsMax     = 3
	webSessionDefaultDMsMax          = 3
	webSessionDefaultFullMax         = 50
	webSessionBookmarkKnownStopCount = 3
)

var (
	xwebOriginOverride string
	xwebHTTPClient     *http.Client
)

type updateFlags struct {
	force    bool
	full     bool
	maxPages int
}

type webUpdateRunner struct {
	r           *runtime
	st          *store.Store
	client      *xweb.Client
	cfg         birdConfig
	now         func() time.Time
	policy      xweb.RateLimitPolicy
	addedTweets int
	addedDMs    int
	rateLimited []string
}

func (r *runtime) runWebSessionUpdateReport(cfg birdConfig) (*updatecontract.TrawlerArchiveUpdateReport, error) {
	var report *updatecontract.TrawlerArchiveUpdateReport
	err := r.withStore(func(st *store.Store) error {
		nowFn := func() time.Time { return time.Now().UTC() }
		now := nowFn()

		// Circuit breaker: do not keep hammering X with an expired session.
		sessionState, err := st.UpdateState(r.ctx, "auth:session_valid")
		if err != nil {
			return err
		}
		if sessionState.Cursor == "false" {
			return render.AccountProviderNoLongerAcceptsConnection(twitterAccountProviderName, twitterDisplayName)
		}

		// Minimum sync interval cooldown so back-to-back or dual-scheduled
		// updates do not waste X web quota.
		if !r.c.updateOpts.force && !r.c.updateOpts.full {
			lastSync, err := st.UpdateState(r.ctx, "web_session_last_sync")
			if err != nil {
				return err
			}
			if !lastSync.LastUpdateAt.IsZero() && now.Sub(lastSync.LastUpdateAt) < webSessionMinSyncInterval {
				if r.log != nil {
					_ = r.log.Info("web_session_cooldown_skip", fmt.Sprintf("last_sync=%s cooldown=%s", lastSync.LastUpdateAt.UTC().Format(time.RFC3339), webSessionMinSyncInterval))
				}
				report = &updatecontract.TrawlerArchiveUpdateReport{
					ArchiveRecordCountAddedByThisUpdate: proto.Uint64(0),
				}
				return nil
			}
		}

		policy := xweb.DefaultRateLimitPolicy()
		client, err := xweb.NewClient(xweb.ClientOptions{
			Origin:      xwebOriginOverride,
			HTTPClient:  r.httpClient(xwebHTTPClient),
			SessionPath: cfg.SessionPath(),
			Policy:      policy,
			Now:         nowFn,
			OnRateLimit: func(obs xweb.RateLimitObservation) {
				r.recordObservedWebRateLimit(st, policy, obs)
			},
		})
		if err != nil {
			return r.webSessionError(err)
		}

		runner := &webUpdateRunner{
			r:      r,
			st:     st,
			client: client,
			cfg:    cfg,
			now:    nowFn,
			policy: policy,
		}
		if err := runner.run(); err != nil {
			var authErr *xweb.WebSessionAuthError
			if errors.As(err, &authErr) {
				_ = st.SetWebSessionValid(r.ctx, false, nowFn())
				return render.AccountProviderNoLongerAcceptsConnection(twitterAccountProviderName, twitterDisplayName)
			}
			return r.webSessionError(err)
		}
		totalAdded := uint64(max(runner.addedTweets+runner.addedDMs, 0))
		report = &updatecontract.TrawlerArchiveUpdateReport{
			ArchiveRecordCountAddedByThisUpdate: proto.Uint64(totalAdded),
		}
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

func (r *runtime) recordObservedWebRateLimit(st *store.Store, policy xweb.RateLimitPolicy, obs xweb.RateLimitObservation) {
	if r.log != nil {
		msg := fmt.Sprintf("endpoint=%s remaining=%d limit=%d rate_limited=%t", obs.Endpoint, obs.Remaining, obs.Limit, obs.RateLimited)
		if !obs.ResetAt.IsZero() {
			msg += " reset_at=" + obs.ResetAt.UTC().Format(time.RFC3339)
		}
		_ = r.log.Info("xweb_rate_limit", msg)
	}
	shouldPersistReset := obs.RateLimited || (obs.HasQuota && obs.Remaining <= policy.RemainingSafetyFloor && !obs.ResetAt.IsZero())
	if shouldPersistReset {
		_ = st.RecordEndpointRateLimit(r.ctx, string(obs.Endpoint), obs.ResetAt, obs.Remaining, obs.Limit, obs.ObservedAt)
	}
}

func (s *webUpdateRunner) run() error {
	account, err := s.verifyAndBindAccount()
	if err != nil {
		return err
	}
	s.reportProgress("identity", 1, 1, "verified @"+account.Username)

	if err := s.syncTimelineFeed(xweb.WebEndpointBookmarks, "bookmarks", "bookmark", s.maxPagesFor(webSessionDefaultIncrementalMax), webSessionBookmarkKnownStopCount, func(cursor string) (xweb.WebTimelinePage, error) {
		return s.client.FetchBookmarksPage(s.r.ctx, webSessionPageSize, cursor)
	}); err != nil {
		return err
	}
	if err := s.syncTimelineFeed(xweb.WebEndpointAuthored, "authored", "authored", s.maxPagesFor(webSessionDefaultIncrementalMax), 1, func(cursor string) (xweb.WebTimelinePage, error) {
		return s.client.FetchUserTweetsPage(s.r.ctx, account.UserID, webSessionPageSize, cursor)
	}); err != nil {
		return err
	}
	if err := s.syncTimelineFeed(xweb.WebEndpointMentions, "mentions", "mention", s.maxPagesFor(webSessionDefaultMentionsMax), 1, func(cursor string) (xweb.WebTimelinePage, error) {
		return s.client.FetchMentionsPage(s.r.ctx, account.Username, webSessionPageSize, cursor)
	}); err != nil {
		return err
	}
	if err := s.syncTimelineFeed(xweb.WebEndpointLikes, "likes", "like", s.maxPagesFor(webSessionDefaultIncrementalMax), 1, func(cursor string) (xweb.WebTimelinePage, error) {
		return s.client.FetchLikesPage(s.r.ctx, account.UserID, webSessionPageSize, cursor)
	}); err != nil {
		return err
	}
	if err := s.syncDirectMessages(account.UserID); err != nil {
		return err
	}

	now := s.now()
	result := "ok"
	if len(s.rateLimited) > 0 {
		result = "partial: rate limited (" + strings.Join(s.rateLimited, ", ") + ")"
	}
	states := []store.UpdateStateUpdate{
		{Kind: "live_update", Cursor: "", LastResult: result, LastUpdateAt: now},
		{Kind: "auth:session_valid", Cursor: "true", LastResult: "true", LastUpdateAt: now},
	}
	if len(s.rateLimited) == 0 {
		states = append(states, store.UpdateStateUpdate{
			Kind:         "web_session_last_sync",
			Cursor:       formatUTCString(now),
			LastResult:   "ok",
			LastUpdateAt: now,
		})
	}
	if err := s.st.CommitLivePage(s.r.ctx, store.LivePage{
		UpdatedAt: now,
		States:    states,
	}); err != nil {
		return err
	}
	s.reportProgress("update", int64(s.addedTweets+s.addedDMs), 0, "X web update complete")
	return nil
}

func (s *webUpdateRunner) verifyAndBindAccount() (xweb.AuthenticatedWebAccount, error) {
	account, err := s.client.VerifyAccount(s.r.ctx)
	if err != nil {
		return xweb.AuthenticatedWebAccount{}, err
	}
	boundOwnerID, err := s.st.OwnerAuthorID(s.r.ctx)
	if err != nil {
		return xweb.AuthenticatedWebAccount{}, err
	}
	if boundOwnerID != "" && boundOwnerID != account.UserID {
		return xweb.AuthenticatedWebAccount{}, render.TrawlerArchiveBelongsToAnotherAccount(twitterDisplayName, twitterAccountProviderName)
	}
	if s.cfg.UserID != "" && s.cfg.UserID != account.UserID {
		return xweb.AuthenticatedWebAccount{}, render.TrawlerArchiveBelongsToAnotherAccount(twitterDisplayName, twitterAccountProviderName)
	}
	if err := s.cfg.SaveIdentity(account.UserID, account.Username); err != nil {
		return xweb.AuthenticatedWebAccount{}, err
	}
	return account, nil
}

func (s *webUpdateRunner) endpointInCooldown(endpoint xweb.WebEndpointName) (time.Time, bool, error) {
	resetAt, err := s.st.EndpointRateLimitResetAt(s.r.ctx, string(endpoint))
	if err != nil {
		return time.Time{}, false, err
	}
	if !resetAt.IsZero() && resetAt.After(s.now()) {
		return resetAt, true, nil
	}
	return time.Time{}, false, nil
}

func (s *webUpdateRunner) syncTimelineFeed(
	endpoint xweb.WebEndpointName,
	phase string,
	role string,
	maxPages int,
	knownStopThreshold int,
	fetchPage func(cursor string) (xweb.WebTimelinePage, error),
) error {
	if resetAt, cooling, err := s.endpointInCooldown(endpoint); err != nil {
		return err
	} else if cooling {
		if s.r.log != nil {
			_ = s.r.log.Info("xweb_endpoint_cooldown_skip", fmt.Sprintf("endpoint=%s reset_at=%s", endpoint, resetAt.UTC().Format(time.RFC3339)))
		}
		s.rateLimited = append(s.rateLimited, string(endpoint))
		return nil
	}

	cursor := ""
	seenCursors := map[string]struct{}{}
	consecutiveKnown := 0
	for pageNum := 0; pageNum < maxPages; pageNum++ {
		page, err := fetchPage(cursor)
		if err != nil {
			var rlErr *xweb.WebRateLimitedError
			if errors.As(err, &rlErr) {
				s.rateLimited = append(s.rateLimited, string(endpoint))
				return nil
			}
			return err
		}

		now := s.now()
		tweets := make([]store.Tweet, 0, len(page.Tweets))
		roles := make([]store.Role, 0, len(page.Tweets))
		profiles := make([]store.Profile, 0, len(page.Profiles))
		for _, p := range page.Profiles {
			if strings.TrimSpace(p.AuthorID) == "" {
				continue
			}
			profiles = append(profiles, store.Profile{
				AuthorID:    p.AuthorID,
				Handle:      p.Handle,
				DisplayName: p.DisplayName,
				LastSeenAt:  now,
			})
		}

		hitKnownBoundary := false
		pageNewCount := 0
		for _, wt := range page.Tweets {
			knownRole, err := s.st.HasRole(s.r.ctx, wt.ID, role)
			if err != nil {
				return err
			}
			if !s.r.c.updateOpts.full && knownRole {
				consecutiveKnown++
				if consecutiveKnown >= knownStopThreshold {
					hitKnownBoundary = true
					break
				}
				continue
			}
			consecutiveKnown = 0
			if !knownRole {
				pageNewCount++
			}
			tweets = append(tweets, storeTweetFromWeb(wt, now))
			roles = append(roles, store.Role{
				TweetID:     wt.ID,
				Role:        role,
				FirstSeenAt: now,
				LastSeenAt:  now,
			})
		}

		if len(tweets) > 0 || len(profiles) > 0 {
			if err := s.st.CommitLivePage(s.r.ctx, store.LivePage{
				Tweets:    tweets,
				Roles:     roles,
				Profiles:  profiles,
				UpdatedAt: now,
			}); err != nil {
				return err
			}
			s.addedTweets += pageNewCount
		}
		s.reportProgress(phase, int64(len(tweets)), int64(len(page.Tweets)), fmt.Sprintf("%s stored %d tweets", phase, len(tweets)))

		if page.RateLimit.HasQuota && page.RateLimit.Remaining <= s.policy.RemainingSafetyFloor && !page.RateLimit.ResetAt.IsZero() {
			s.rateLimited = append(s.rateLimited, string(endpoint))
			return nil
		}
		if hitKnownBoundary || page.NextCursor == "" || len(page.Tweets) == 0 {
			return nil
		}
		if _, dup := seenCursors[page.NextCursor]; dup {
			return nil
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return nil
}

func (s *webUpdateRunner) syncDirectMessages(ownerUserID string) error {
	if resetAt, cooling, err := s.endpointInCooldown(xweb.WebEndpointDMs); err != nil {
		return err
	} else if cooling {
		if s.r.log != nil {
			_ = s.r.log.Info("xweb_endpoint_cooldown_skip", fmt.Sprintf("endpoint=dms reset_at=%s", resetAt.UTC().Format(time.RFC3339)))
		}
		s.rateLimited = append(s.rateLimited, string(xweb.WebEndpointDMs))
		return nil
	}

	initial, err := s.client.FetchDirectMessageInboxInitialState(s.r.ctx, ownerUserID)
	if err != nil {
		var rlErr *xweb.WebRateLimitedError
		if errors.As(err, &rlErr) {
			s.rateLimited = append(s.rateLimited, string(xweb.WebEndpointDMs))
			return nil
		}
		return err
	}
	addedInitial, err := s.commitDMPage(initial)
	if err != nil {
		return err
	}
	s.addedDMs += addedInitial
	s.reportProgress("dms", int64(addedInitial), int64(len(initial.Messages)), fmt.Sprintf("dms stored %d new messages", addedInitial))

	if initial.RateLimit.HasQuota && initial.RateLimit.Remaining <= s.policy.RemainingSafetyFloor && !initial.RateLimit.ResetAt.IsZero() {
		s.rateLimited = append(s.rateLimited, string(xweb.WebEndpointDMs))
		return nil
	}

	// Only paginate older inbox_timeline pages when full backfill is requested
	// or every message on the initial inbox page was unseen.
	shouldPaginate := s.r.c.updateOpts.full || (len(initial.Messages) > 0 && addedInitial == len(initial.Messages))
	if !shouldPaginate {
		return nil
	}
	maxPages := s.maxPagesFor(webSessionDefaultDMsMax)
	for _, stream := range []struct {
		kind   string
		cursor string
	}{
		{kind: "trusted", cursor: initial.TrustedCursor},
		{kind: "untrusted", cursor: initial.UntrustedCursor},
	} {
		cursor := stream.cursor
		seenCursors := map[string]struct{}{}
		for pageNum := 0; cursor != "" && pageNum < maxPages; pageNum++ {
			if _, dup := seenCursors[cursor]; dup {
				break
			}
			seenCursors[cursor] = struct{}{}
			page, err := s.client.FetchDirectMessageInboxTimeline(s.r.ctx, ownerUserID, stream.kind, cursor)
			if err != nil {
				var rlErr *xweb.WebRateLimitedError
				if errors.As(err, &rlErr) {
					s.rateLimited = append(s.rateLimited, string(xweb.WebEndpointDMs))
					return nil
				}
				return err
			}
			added, err := s.commitDMPage(page)
			if err != nil {
				return err
			}
			s.addedDMs += added
			s.reportProgress("dms", int64(added), int64(len(page.Messages)), fmt.Sprintf("dms (%s) stored %d new messages", stream.kind, added))
			if page.RateLimit.HasQuota && page.RateLimit.Remaining <= s.policy.RemainingSafetyFloor && !page.RateLimit.ResetAt.IsZero() {
				s.rateLimited = append(s.rateLimited, string(xweb.WebEndpointDMs))
				return nil
			}
			if !s.r.c.updateOpts.full && added == 0 {
				break
			}
			next := page.TrustedCursor
			if stream.kind == "untrusted" {
				next = page.UntrustedCursor
			}
			cursor = next
		}
	}
	return nil
}

func (s *webUpdateRunner) commitDMPage(page xweb.WebDirectMessageInboxPage) (int, error) {
	now := s.now()
	profiles := make([]store.Profile, 0, len(page.Profiles))
	for _, p := range page.Profiles {
		profiles = append(profiles, store.Profile{
			AuthorID:    p.AuthorID,
			Handle:      p.Handle,
			DisplayName: p.DisplayName,
			LastSeenAt:  now,
		})
	}
	convs := make([]store.DirectMessageConversation, 0, len(page.Conversations))
	for _, c := range page.Conversations {
		convs = append(convs, store.DirectMessageConversation{
			ID:                   c.ID,
			InboxKind:            c.InboxKind,
			ConversationKind:     c.ConversationKind,
			Title:                c.Title,
			ParticipantAuthorID:  c.ParticipantAuthorID,
			ParticipantAuthorIDs: c.ParticipantAuthorIDs,
			UnreadCount:          c.UnreadCount,
			LastMessageAt:        c.LastMessageAt,
		})
	}
	msgs := make([]store.DirectMessage, 0, len(page.Messages))
	for _, m := range page.Messages {
		msgs = append(msgs, store.DirectMessage{
			ID:             m.ID,
			ConversationID: m.ConversationID,
			CreatedAt:      m.CreatedAt,
			SenderAuthorID: m.SenderAuthorID,
			Direction:      m.Direction,
			Text:           m.Text,
			MediaCount:     m.MediaCount,
			RawJSON:        m.RawJSON,
		})
	}
	return s.st.CommitDirectMessageBatch(s.r.ctx, store.DirectMessageBatch{
		Profiles:      profiles,
		Conversations: convs,
		Messages:      msgs,
		UpdatedAt:     now,
	})
}

func (s *webUpdateRunner) maxPagesFor(defaultIncremental int) int {
	if s.r.c.updateOpts.maxPages > 0 {
		return s.r.c.updateOpts.maxPages
	}
	if s.r.c.updateOpts.full {
		return webSessionDefaultFullMax
	}
	return defaultIncremental
}

func (s *webUpdateRunner) reportProgress(phase string, done, total int64, message string) {
	if s.r.req == nil || s.r.req.ReportTrawlerCommandProgress == nil {
		return
	}
	s.r.req.ReportTrawlerCommandProgress(trawlkit.Progress{
		Phase:   phase,
		Done:    done,
		Total:   total,
		Message: message,
	})
}

func storeTweetFromWeb(wt xweb.WebTweet, now time.Time) store.Tweet {
	return store.Tweet{
		ID:               wt.ID,
		CreatedAt:        wt.CreatedAt,
		AuthorID:         wt.AuthorID,
		AuthorHandle:     wt.AuthorHandle,
		AuthorName:       wt.AuthorName,
		Text:             wt.Text,
		InReplyToID:      wt.InReplyToID,
		ConversationID:   wt.ConversationID,
		QuotedTweetID:    wt.QuotedTweetID,
		LikeCount:        wt.LikeCount,
		RetweetCount:     wt.RetweetCount,
		ReplyCount:       wt.ReplyCount,
		ViewCount:        wt.ViewCount,
		QuoteCount:       wt.QuoteCount,
		BookmarkCount:    wt.BookmarkCount,
		HasMedia:         wt.HasMedia,
		RawJSON:          wt.RawJSON,
		FirstSource:      "live",
		MetricsFetchedAt: now,
	}
}

func formatUTCString(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
