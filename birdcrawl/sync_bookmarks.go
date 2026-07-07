package birdcrawl

import (
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/xapi"
)

func (s *syncRunner) syncBookmarks() error {
	active, err := s.st.SyncState(s.r.ctx, "bookmark_pass_active")
	if err != nil {
		return err
	}
	last, err := s.st.SyncState(s.r.ctx, "bookmark_pass")
	if err != nil {
		return err
	}
	// A full pass bills every bookmark ($0.001 each), so it runs at most
	// weekly; that is also when removals are detected. Between passes,
	// new bookmarks are picked up incrementally, stopping at the first
	// already-known one — at daily cadence this is the difference between
	// ~$12/month and ~$1/month at a few hundred bookmarks.
	if active.Cursor == "" && last.Cursor != "" && s.now().Sub(last.LastSyncAt) < 6*24*time.Hour {
		return s.syncBookmarksIncremental(last.Cursor)
	}
	pass := active.Cursor
	if pass == "" {
		pass = s.nextBookmarkPass(last.Cursor)
	}
	pageState, err := s.st.SyncState(s.r.ctx, "page:bookmarks")
	if err != nil {
		return err
	}
	token := pageState.Cursor
	for {
		if err := s.beforeRequest(pageSize * xapi.PriceOwnedPostMicros); err != nil {
			return err
		}
		page, err := s.client.Bookmarks(s.r.ctx, s.cfg.UserID, xapi.PageQuery{PaginationToken: token, MaxResults: pageSize})
		if err != nil {
			return err
		}
		passTime := parseSyncTime(pass)
		batch, err := s.convertPage("bookmarks", page, "bookmark", passTime)
		if err != nil {
			return err
		}
		now := s.now()
		states := []store.SyncStateUpdate{
			{Kind: "bookmark_pass_active", Cursor: pass, LastResult: "running", LastSyncAt: now},
			{Kind: "page:bookmarks", Cursor: page.NextToken, LastResult: "partial", LastSyncAt: now},
			{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now},
		}
		complete := page.NextToken == ""
		if complete {
			states = []store.SyncStateUpdate{
				{Kind: "bookmark_pass", Cursor: pass, LastResult: "ok", LastSyncAt: now},
				{Kind: "bookmark_pass_active", Cursor: "", LastResult: "ok", LastSyncAt: now},
				{Kind: "page:bookmarks", Cursor: "", LastResult: "ok", LastSyncAt: now},
				{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now},
			}
		}
		if err := s.commitBatch(batch, page.Charge, states, now); err != nil {
			return err
		}
		if err := s.printBatch("bookmarks", batch, page, complete); err != nil {
			return err
		}
		if complete {
			return nil
		}
		token = page.NextToken
	}
}

// syncBookmarksIncremental stores bookmarks newer than the last full pass.
// New rows are stamped with the last full pass id so the current-bookmark
// count (rows stamped with that pass) includes them until the next full pass.
func (s *syncRunner) syncBookmarksIncremental(pass string) error {
	token := ""
	for {
		if err := s.beforeRequest(pageSize * xapi.PriceOwnedPostMicros); err != nil {
			return err
		}
		page, err := s.client.Bookmarks(s.r.ctx, s.cfg.UserID, xapi.PageQuery{PaginationToken: token, MaxResults: pageSize})
		if err != nil {
			return err
		}
		cut := len(page.Tweets)
		for i, tweet := range page.Tweets {
			known, err := s.st.HasRole(s.r.ctx, tweet.ID, "bookmark")
			if err != nil {
				return err
			}
			if known {
				cut = i
				break
			}
		}
		trimmed := cut < len(page.Tweets)
		page.Tweets = page.Tweets[:cut]
		batch, err := s.convertPage("bookmarks", page, "bookmark", parseSyncTime(pass))
		if err != nil {
			return err
		}
		now := s.now()
		states := []store.SyncStateUpdate{
			{Kind: "auth:token_valid", Cursor: "true", LastResult: "true", LastSyncAt: now},
		}
		complete := trimmed || page.NextToken == ""
		if err := s.commitBatch(batch, page.Charge, states, now); err != nil {
			return err
		}
		if err := s.printBatch("bookmarks", batch, page, complete); err != nil {
			return err
		}
		if complete {
			return nil
		}
		token = page.NextToken
	}
}
