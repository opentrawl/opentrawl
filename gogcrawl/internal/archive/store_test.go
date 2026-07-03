package archive

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/whomatch"
)

func TestStoreSearchOpenStatus(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	result, err := st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Alice", FromAddress: "alice@example.com", ToAddress: "bob@example.com", CcAddress: "carol@example.com", Subject: "Project sync", Body: "The project sync is Friday.", Labels: []string{"INBOX"}},
		{ID: "m2", ThreadID: "t2", Time: now.Add(-time.Hour), FromName: "Bob", FromAddress: "bob@example.com", Subject: "Lunch", Body: "Lunch moved later.", Labels: []string{"SENT"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 2 {
		t.Fatalf("inserted = %d, want 2", result.Inserted)
	}
	if err := st.MarkSyncStarted(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSyncCompleted(ctx, now); err != nil {
		t.Fatal(err)
	}
	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 2 || status.Senders != 2 || status.Since != 2026 {
		t.Fatalf("status = %#v", status)
	}
	search, err := st.Search(ctx, SearchOptions{Query: "project", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 1 || search.Truncated || len(search.Results) != 1 {
		t.Fatalf("search = %#v", search)
	}
	if search.Results[0].Ref != RefPrefix+"m1" || search.Results[0].Who != "Alice" {
		t.Fatalf("hit = %#v", search.Results[0])
	}
	if strings.ContainsAny(search.Results[0].Snippet, "[]") {
		t.Fatalf("snippet has marker brackets: %q", search.Results[0].Snippet)
	}
	open, err := st.OpenMessage(ctx, RefPrefix+"m1")
	if err != nil {
		t.Fatal(err)
	}
	if open.Headers.ToAddress != "bob@example.com" || open.Headers.CcAddress != "carol@example.com" {
		t.Fatalf("headers = %#v", open.Headers)
	}
	if open.Body != "The project sync is Friday." {
		t.Fatalf("body = %q", open.Body)
	}
}

func TestInsertMessagesIgnoresExistingIDs(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	msg := Message{ID: "m1", ThreadID: "t1", Time: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), FromAddress: "alice@example.com", Subject: "Hello", Body: "Hello"}
	first, err := st.InsertMessages(ctx, []Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.InsertMessages(ctx, []Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	if first.Inserted != 1 || second.Inserted != 0 {
		t.Fatalf("insert results = %#v %#v", first, second)
	}
}

func TestSearchWhoFiltersParticipantsAndFoldsOwner(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	if err := st.SetOwnerAccount(ctx, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Vendor Example", FromAddress: "vendor@example.com", ToAddress: "Owner Example <owner@example.com>", Subject: "Invoice needle", Body: "Needle body.", Labels: []string{"INBOX"}},
		{ID: "m2", ThreadID: "t2", Time: now.Add(time.Minute), FromName: "Owner Alias", FromAddress: "alias@example.com", ToAddress: "Vendor Example <vendor@example.com>", Subject: "Sent needle", Body: "Needle reply.", Labels: []string{"SENT"}},
		{ID: "m3", ThreadID: "t3", Time: now.Add(2 * time.Minute), FromName: "Other Example", FromAddress: "other@example.com", Subject: "Needle", Body: "Not the owner.", Labels: []string{"INBOX"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerSearch, err := st.Search(ctx, SearchOptions{Query: "needle", Who: " me "})
	if err != nil {
		t.Fatal(err)
	}
	if ownerSearch.TotalMatches != 2 || ownerSearch.WhoResolved == nil || ownerSearch.WhoResolved.Who != "me" {
		t.Fatalf("owner search = %#v", ownerSearch)
	}
	foundMeSender := false
	for _, hit := range ownerSearch.Results {
		if hit.Ref == RefPrefix+"m2" && hit.Who == "me" {
			foundMeSender = true
		}
	}
	if !foundMeSender {
		t.Fatalf("sent owner alias was not displayed as me: %#v", ownerSearch.Results)
	}
	vendorSearch, err := st.Search(ctx, SearchOptions{Query: "needle", Who: "  vendor \t example "})
	if err != nil {
		t.Fatal(err)
	}
	if vendorSearch.TotalMatches != 2 || vendorSearch.WhoResolved == nil || vendorSearch.WhoResolved.Who != "Vendor Example" {
		t.Fatalf("vendor search = %#v", vendorSearch)
	}
	aliasSearch, err := st.Search(ctx, SearchOptions{Query: "needle", Who: "ALIAS@EXAMPLE.COM"})
	if err != nil {
		t.Fatal(err)
	}
	if aliasSearch.TotalMatches != 1 || aliasSearch.Results[0].Ref != RefPrefix+"m2" || aliasSearch.WhoResolved != nil {
		t.Fatalf("alias search = %#v", aliasSearch)
	}
}

func TestResolveWhoDedupesAndMatchesGenerously(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Alice Example", FromAddress: "alice@example.com", Subject: "Needle", Body: "First."},
		{ID: "m2", ThreadID: "t2", Time: now.Add(time.Minute), FromName: "Alice A.", FromAddress: "alice@example.com", Subject: "Needle", Body: "Second."},
		{ID: "m3", ThreadID: "t3", Time: now.Add(2 * time.Minute), FromName: "Alicia Example", FromAddress: "alicia@example.com", Subject: "Needle", Body: "Third."},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := st.ResolveWho(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Candidates) != 2 {
		t.Fatalf("resolved = %#v", resolved)
	}
	candidate := resolved.Candidates[0]
	if candidate.Who != "Alice A." || len(candidate.Identifiers) != 1 || candidate.Identifiers[0] != "alice@example.com" || candidate.Messages != 2 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if resolved.Candidates[1].Who != "Alicia Example" {
		t.Fatalf("generous candidate = %#v", resolved.Candidates[1])
	}
	closeSpelling, err := st.ResolveWho(ctx, "alce")
	if err != nil {
		t.Fatal(err)
	}
	// Tightened distance thresholds: "alce" is within 1 edit of
	// "alice" but 3 edits from "alicia", which no longer matches.
	if len(closeSpelling.Candidates) != 1 || closeSpelling.Candidates[0].Who != "Alice A." {
		t.Fatalf("close spelling = %#v", closeSpelling)
	}
	prefix, err := st.ResolveWho(ctx, "ali")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefix.Candidates) != 2 {
		t.Fatalf("prefix = %#v", prefix)
	}
}

func TestResolveWhoMergesSameDisplayCandidates(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Michael Palmer", FromAddress: "michael.gmail@example.com", Subject: "First", Body: "First."},
		{ID: "m2", ThreadID: "t2", Time: now.Add(time.Minute), FromName: "  MICHAEL   PALMER  ", FromAddress: "michael.icloud@example.com", Subject: "Second", Body: "Second."},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := st.ResolveWho(ctx, "michael palmer")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Candidates) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	candidate := resolved.Candidates[0]
	if whomatch.Normalize(candidate.Who) != "michael palmer" || candidate.Messages != 2 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if len(candidate.Identifiers) != 2 || candidate.Identifiers[0] != "michael.gmail@example.com" || candidate.Identifiers[1] != "michael.icloud@example.com" {
		t.Fatalf("identifiers = %#v", candidate.Identifiers)
	}
	byIdentifier, err := st.ResolveWho(ctx, "michael.icloud@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(byIdentifier.Candidates) != 1 {
		t.Fatalf("by identifier = %#v", byIdentifier)
	}
	identifiers := byIdentifier.Candidates[0].Identifiers
	if len(identifiers) != 2 || identifiers[0] != "michael.icloud@example.com" || identifiers[1] != "michael.gmail@example.com" {
		t.Fatalf("matching identifier was not first: %#v", identifiers)
	}
}

func TestResolveWhoIgnoresHiddenGroupedNames(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now.Add(-time.Minute), FromName: "Michael Example via network", FromAddress: "messages-noreply@network.example.com", Subject: "Older", Body: "First."},
		{ID: "m2", ThreadID: "t2", Time: now, FromName: "Network Alerts", FromAddress: "messages-noreply@network.example.com", Subject: "Newer", Body: "Second."},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := st.ResolveWho(ctx, "michael")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Candidates) != 0 {
		t.Fatalf("resolved hidden grouped name = %#v", resolved)
	}
	visible, err := st.ResolveWho(ctx, "network")
	if err != nil {
		t.Fatal(err)
	}
	if len(visible.Candidates) != 1 || visible.Candidates[0].Who != "Network Alerts" || visible.Candidates[0].Identifiers[0] != "messages-noreply@network.example.com" {
		t.Fatalf("visible resolution = %#v", visible)
	}
}

func TestResolveWhoShowsMatchingIdentifierFirst(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Owner Example", FromAddress: "owner@example.com", Subject: "First", Body: "First.", Labels: []string{"SENT"}},
		{ID: "m2", ThreadID: "t2", Time: now.Add(time.Minute), FromName: "Work Example", FromAddress: "work.person@example.com", Subject: "Second", Body: "Second.", Labels: []string{"SENT"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := st.ResolveWho(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Candidates) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	candidate := resolved.Candidates[0]
	if candidate.Who != "me" || len(candidate.Identifiers) < 2 || candidate.Identifiers[0] != "work.person@example.com" {
		t.Fatalf("candidate = %#v", candidate)
	}
}

func TestSearchWhoAmbiguityReportsCandidates(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Casey One", FromAddress: "casey.one@example.com", Subject: "Needle", Body: "First."},
		{ID: "m2", ThreadID: "t2", Time: now.Add(time.Minute), FromName: "Casey Two", FromAddress: "casey.two@example.com", Subject: "Needle", Body: "Second."},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Search(ctx, SearchOptions{Query: "needle", Who: "casey"})
	var ambiguous *AmbiguousWhoError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %v, want AmbiguousWhoError", err)
	}
	if len(ambiguous.Candidates) != 2 || ambiguous.Candidates[0].Who != "Casey Two" || ambiguous.Candidates[1].Who != "Casey One" {
		t.Fatalf("candidates = %#v", ambiguous.Candidates)
	}
}

func TestSearchWhoCloseSpellingSingleCandidateSuggests(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Dana Example", FromAddress: "dana@example.com", Subject: "Needle", Body: "First."},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Search(ctx, SearchOptions{Query: "needle", Who: "danna"})
	var unknown *UnknownWhoError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want UnknownWhoError", err)
	}
	if len(unknown.DidYouMean) != 1 || unknown.DidYouMean[0].Who != "Dana Example" {
		t.Fatalf("did_you_mean = %#v", unknown.DidYouMean)
	}
}

func TestSearchSnippetMarksMidTokenCuts(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	body := strings.Repeat("a", 70) + " needle " + strings.Repeat("b", 220)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", Time: time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC), FromName: "Alice", FromAddress: "alice@example.com", Subject: "Receipt", Body: body},
	})
	if err != nil {
		t.Fatal(err)
	}
	search, err := st.Search(ctx, SearchOptions{Query: "needle", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("results = %#v", search.Results)
	}
	snippet := search.Results[0].Snippet
	if snippet != "…needle…" {
		t.Fatalf("snippet should snap cuts to token boundaries: %q", snippet)
	}
	if !strings.Contains(snippet, "needle") {
		t.Fatalf("snippet lost match: %q", snippet)
	}
}

func TestSearchFilterOnlyUsesNewestMatchingItems(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "old", ThreadID: "t1", Time: now.Add(-2 * time.Hour), FromName: "Alice", FromAddress: "alice@example.com", Subject: "Old", Body: "First."},
		{ID: "new", ThreadID: "t2", Time: now, FromName: "Alice", FromAddress: "alice@example.com", Subject: "New", Body: "Second."},
		{ID: "other", ThreadID: "t3", Time: now.Add(time.Hour), FromName: "Bob", FromAddress: "bob@example.com", Subject: "Other", Body: "Third."},
	})
	if err != nil {
		t.Fatal(err)
	}
	search, err := st.Search(ctx, SearchOptions{Who: "alice@example.com", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if search.Query != "" || search.TotalMatches != 2 || !search.Truncated || len(search.Results) != 1 || search.Results[0].Ref != RefPrefix+"new" {
		t.Fatalf("filter-only search = %#v", search)
	}
}

func TestShortRefsResolveAndFailSafely(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	_, err = st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Alice", FromAddress: "alice@example.com", Subject: "Project needle", Body: "First."},
		{ID: "m2", ThreadID: "t2", Time: now.Add(time.Minute), FromName: "Bob", FromAddress: "bob@example.com", Subject: "Project needle", Body: "Second."},
	})
	if err != nil {
		t.Fatal(err)
	}
	search, err := st.Search(ctx, SearchOptions{Query: "needle", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Results[0].ShortRef == "" || search.Results[0].Ref == search.Results[0].ShortRef {
		t.Fatalf("short ref search = %#v", search)
	}
	open, err := st.OpenMessage(ctx, search.Results[0].ShortRef)
	if err != nil {
		t.Fatal(err)
	}
	if open.Ref != search.Results[0].Ref {
		t.Fatalf("open ref = %q, want %q", open.Ref, search.Results[0].Ref)
	}
	if _, err := st.RebuildShortRefs(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `
insert into short_refs(alias, full_ref)
values ('22222', ?), ('22222', ?)
`, RefPrefix+"m1", RefPrefix+"m2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.OpenMessage(ctx, "22222"); !errors.Is(err, ErrAmbiguousShortRef) {
		t.Fatalf("ambiguous error = %v", err)
	}
	if _, err := st.OpenMessage(ctx, "33333"); !errors.Is(err, ErrUnknownShortRef) {
		t.Fatalf("unknown error = %v", err)
	}
}
