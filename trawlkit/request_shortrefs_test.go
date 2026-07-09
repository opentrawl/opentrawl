package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestShortRefsSurviveReducedKindRebuild(t *testing.T) {
	ctx := context.Background()
	_, req := openShortRefTestRequest(t, ctx)

	const (
		messageRef = "source:msg/1"
		chatRef    = "source:chat/1"
	)
	records := []ShortRefRecord{
		{Kind: "source:msg/"},
		{Kind: "source:chat/"},
		{Ref: messageRef},
		{Ref: chatRef},
	}
	if _, err := req.RebuildShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{messageRef, chatRef})
	if err != nil {
		t.Fatal(err)
	}
	chatAlias := aliases[chatRef]
	if chatAlias == "" {
		t.Fatalf("chat alias missing: %#v", aliases)
	}

	reducedRecords := []ShortRefRecord{{Ref: messageRef}}
	if _, err := req.RebuildShortRefs(ctx, reducedRecords); err != nil {
		t.Fatal(err)
	}
	resolved, err := req.ResolveShortRef(ctx, chatAlias)
	if err != nil {
		t.Fatalf("ResolveShortRef(%q): %v", chatAlias, err)
	}
	if len(resolved) != 1 || resolved[0] != chatRef {
		t.Fatalf("ResolveShortRef(%q) = %#v, want %q", chatAlias, resolved, chatRef)
	}
	after, err := req.ShortRefAliases(ctx, []string{messageRef, chatRef})
	if err != nil {
		t.Fatal(err)
	}
	if after[chatRef] != chatAlias {
		t.Fatalf("chat alias changed after reduced rebuild: got %q want %q", after[chatRef], chatAlias)
	}
	if after[messageRef] == "" {
		t.Fatalf("message alias missing after reduced rebuild: %#v", after)
	}
}

func TestShortRefsFreshSyncClearsRegeneratedKind(t *testing.T) {
	ctx := context.Background()
	_, req := openShortRefTestRequest(t, ctx)

	const (
		kind      = "source:item/"
		firstRef  = "source:item/first"
		secondRef = "source:item/second"
	)
	if _, err := req.RebuildShortRefs(ctx, []ShortRefRecord{{Kind: kind}, {Ref: firstRef}}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{firstRef})
	if err != nil {
		t.Fatal(err)
	}
	firstAlias := aliases[firstRef]
	if firstAlias == "" {
		t.Fatalf("first alias missing: %#v", aliases)
	}

	if _, err := req.RebuildShortRefs(ctx, []ShortRefRecord{{Kind: kind}}); err != nil {
		t.Fatal(err)
	}
	if _, err := req.ResolveShortRef(ctx, firstAlias); !errors.Is(err, ErrUnknownShortRef) {
		t.Fatalf("ResolveShortRef(%q) err = %v, want ErrUnknownShortRef", firstAlias, err)
	}

	if _, err := req.RebuildShortRefs(ctx, []ShortRefRecord{{Kind: kind}, {Ref: secondRef}}); err != nil {
		t.Fatal(err)
	}
	aliases, err = req.ShortRefAliases(ctx, []string{secondRef})
	if err != nil {
		t.Fatal(err)
	}
	secondAlias := aliases[secondRef]
	if secondAlias == "" {
		t.Fatalf("second alias missing: %#v", aliases)
	}
	resolved, err := req.ResolveShortRef(ctx, secondAlias)
	if err != nil {
		t.Fatalf("ResolveShortRef(%q): %v", secondAlias, err)
	}
	if len(resolved) != 1 || resolved[0] != secondRef {
		t.Fatalf("ResolveShortRef(%q) = %#v, want %q", secondAlias, resolved, secondRef)
	}
}

func TestShortRefsExtendRegeneratedAliasPastSurvivingAlias(t *testing.T) {
	ctx := context.Background()
	st, req := openShortRefTestRequest(t, ctx)

	const (
		messageKind  = "source:msg/"
		messageRef   = "source:msg/1"
		survivingRef = "source:foreign/1"
	)
	survivingAlias := shortref.Alias(messageRef, shortref.MinLength)
	if err := shortref.EnsureSchema(ctx, st.DB()); err != nil {
		t.Fatal(err)
	}
	if err := shortref.NewSQLiteIndex(st.DB()).Upsert(ctx, survivingAlias, survivingRef); err != nil {
		t.Fatal(err)
	}

	if _, err := req.RebuildShortRefs(ctx, []ShortRefRecord{{Kind: messageKind}, {Ref: messageRef}}); err != nil {
		t.Fatal(err)
	}

	aliases, err := req.ShortRefAliases(ctx, []string{messageRef, survivingRef})
	if err != nil {
		t.Fatal(err)
	}
	messageAlias := aliases[messageRef]
	if messageAlias == "" {
		t.Fatalf("message alias missing: %#v", aliases)
	}
	if messageAlias == survivingAlias || !strings.HasPrefix(messageAlias, survivingAlias) {
		t.Fatalf("message alias = %q, want extension of surviving alias %q", messageAlias, survivingAlias)
	}
	if aliases[survivingRef] != survivingAlias {
		t.Fatalf("surviving alias = %q, want %q", aliases[survivingRef], survivingAlias)
	}
	assertShortRefResolves(t, ctx, req, messageAlias, messageRef)
	assertShortRefResolves(t, ctx, req, survivingAlias, survivingRef)
}

func TestShortRefsClearKindHonorsPrefixTerminator(t *testing.T) {
	ctx := context.Background()
	_, req := openShortRefTestRequest(t, ctx)

	const (
		messageKind     = "source:msg/"
		messageExtraRef = "source:msgextra/1"
		messageRef      = "source:msg/1"
	)
	if _, err := req.RebuildShortRefs(ctx, []ShortRefRecord{
		{Kind: messageKind},
		{Kind: "source:msgextra/"},
		{Ref: messageRef},
		{Ref: messageExtraRef},
	}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{messageRef, messageExtraRef})
	if err != nil {
		t.Fatal(err)
	}
	messageAlias := aliases[messageRef]
	messageExtraAlias := aliases[messageExtraRef]
	if messageAlias == "" || messageExtraAlias == "" {
		t.Fatalf("aliases missing: %#v", aliases)
	}

	if _, err := req.RebuildShortRefs(ctx, []ShortRefRecord{{Kind: messageKind}}); err != nil {
		t.Fatal(err)
	}

	if _, err := req.ResolveShortRef(ctx, messageAlias); !errors.Is(err, ErrUnknownShortRef) {
		t.Fatalf("ResolveShortRef(%q) err = %v, want ErrUnknownShortRef", messageAlias, err)
	}
	assertShortRefResolves(t, ctx, req, messageExtraAlias, messageExtraRef)
}

func TestShortRefsIdempotentResync(t *testing.T) {
	ctx := context.Background()
	st, req := openShortRefTestRequest(t, ctx)

	refs := []string{"source:msg/1", "source:msg/2", "source:chat/1"}
	records := []ShortRefRecord{
		{Kind: "source:msg/"},
		{Kind: "source:chat/"},
		{Ref: refs[0]},
		{Ref: refs[1]},
		{Ref: refs[2]},
	}
	if _, err := req.RebuildShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	aliasesBefore, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	rowsBefore := countShortRefRows(t, ctx, st)

	if _, err := req.RebuildShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	aliasesAfter, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	rowsAfter := countShortRefRows(t, ctx, st)

	if !reflect.DeepEqual(aliasesAfter, aliasesBefore) {
		t.Fatalf("aliases changed after idempotent resync: got %#v want %#v", aliasesAfter, aliasesBefore)
	}
	if rowsAfter != rowsBefore {
		t.Fatalf("short_refs rows changed after idempotent resync: got %d want %d", rowsAfter, rowsBefore)
	}
}

func TestShortRefsPreserveCanonicalAliasSemanticsAtScale(t *testing.T) {
	ctx := context.Background()
	_, req := openShortRefTestRequest(t, ctx)

	const refCount = 6000
	refs := make([]string, 0, refCount)
	records := make([]ShortRefRecord, 0, refCount)
	for i := range refCount {
		ref := fmt.Sprintf("source:item/%06d", i)
		refs = append(refs, ref)
		records = append(records, ShortRefRecord{Ref: ref})
	}

	oldEntries, err := shortref.BuildSlice(refs)
	if err != nil {
		t.Fatal(err)
	}
	oldAliases := make(map[string]string, len(oldEntries))
	for _, entry := range oldEntries {
		oldAliases[entry.FullRef] = entry.Alias
	}

	if rebuilt, err := req.RebuildShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	} else if rebuilt != refCount {
		t.Fatalf("rebuilt %d refs, want %d", rebuilt, refCount)
	}

	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		alias := oldAliases[ref]
		if alias == "" {
			t.Fatalf("missing old alias for %q", ref)
		}
		if aliases[ref] != alias {
			t.Fatalf("alias for %q changed: got %q want %q", ref, aliases[ref], alias)
		}
		resolved, err := req.ResolveShortRef(ctx, alias)
		if err != nil {
			t.Fatalf("ResolveShortRef(%q) for %q: %v", alias, ref, err)
		}
		if len(resolved) != 1 || resolved[0] != ref {
			t.Fatalf("ResolveShortRef(%q) = %#v, want %q", alias, resolved, ref)
		}
	}
}

func openShortRefTestRequest(t *testing.T, ctx context.Context) (*ckstore.Store, *Request) {
	t.Helper()
	st, err := ckstore.Open(ctx, ckstore.Options{Path: filepath.Join(t.TempDir(), "archive.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, &Request{Store: st}
}

func countShortRefRows(t *testing.T, ctx context.Context, st *ckstore.Store) int {
	t.Helper()
	var rows int
	if err := st.DB().QueryRowContext(ctx, `select count(*) from short_refs`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertShortRefResolves(t *testing.T, ctx context.Context, req *Request, alias, want string) {
	t.Helper()
	resolved, err := req.ResolveShortRef(ctx, alias)
	if err != nil {
		t.Fatalf("ResolveShortRef(%q): %v", alias, err)
	}
	if len(resolved) != 1 || resolved[0] != want {
		t.Fatalf("ResolveShortRef(%q) = %#v, want %q", alias, resolved, want)
	}
}
