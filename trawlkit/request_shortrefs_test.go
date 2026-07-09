package trawlkit

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestShortRefsSurviveReducedSync(t *testing.T) {
	ctx := context.Background()
	_, req := openShortRefTestRequest(t, ctx)

	const (
		messageRef = "source:msg/1"
		chatRef    = "source:chat/1"
	)
	records := []ShortRefRecord{
		{Ref: messageRef},
		{Ref: chatRef},
	}
	if _, err := req.AssignShortRefs(ctx, records); err != nil {
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

	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: messageRef}}); err != nil {
		t.Fatal(err)
	}
	assertShortRefResolves(t, ctx, req, chatAlias, chatRef)
	after, err := req.ShortRefAliases(ctx, []string{messageRef, chatRef})
	if err != nil {
		t.Fatal(err)
	}
	if after[chatRef] != chatAlias {
		t.Fatalf("chat alias changed after reduced sync: got %q want %q", after[chatRef], chatAlias)
	}
	if after[messageRef] != aliases[messageRef] {
		t.Fatalf("message alias changed after reduced sync: got %q want %q", after[messageRef], aliases[messageRef])
	}
}

func TestShortRefsEmptySyncDeletesNothing(t *testing.T) {
	ctx := context.Background()
	st, req := openShortRefTestRequest(t, ctx)

	const ref = "source:item/deleted"
	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{ref})
	if err != nil {
		t.Fatal(err)
	}
	alias := aliases[ref]
	if alias == "" {
		t.Fatalf("alias missing: %#v", aliases)
	}
	rowsBefore := countShortRefRows(t, ctx, st)

	if _, err := req.AssignShortRefs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertShortRefResolves(t, ctx, req, alias, ref)
	if rowsAfter := countShortRefRows(t, ctx, st); rowsAfter != rowsBefore {
		t.Fatalf("short_refs rows changed after empty sync: got %d want %d", rowsAfter, rowsBefore)
	}
}

func TestShortRefsExtendNewAliasPastStoredAlias(t *testing.T) {
	ctx := context.Background()
	st, req := openShortRefTestRequest(t, ctx)

	const (
		olderRef = "source:item/older"
		newerRef = "source:item/newer"
	)
	storedAlias := shortref.Alias(newerRef, shortref.MinLength)
	if err := shortref.EnsureSchema(ctx, st.DB()); err != nil {
		t.Fatal(err)
	}
	if err := shortref.NewSQLiteIndex(st.DB()).Upsert(ctx, storedAlias, olderRef); err != nil {
		t.Fatal(err)
	}

	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: newerRef}}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{olderRef, newerRef})
	if err != nil {
		t.Fatal(err)
	}
	newAlias := aliases[newerRef]
	if newAlias == "" {
		t.Fatalf("newer alias missing: %#v", aliases)
	}
	if newAlias == storedAlias || !strings.HasPrefix(newAlias, storedAlias) || len(newAlias) != len(storedAlias)+1 {
		t.Fatalf("newer alias = %q, want one-character extension of stored alias %q", newAlias, storedAlias)
	}
	if aliases[olderRef] != storedAlias {
		t.Fatalf("older alias = %q, want %q", aliases[olderRef], storedAlias)
	}
	assertShortRefResolves(t, ctx, req, storedAlias, olderRef)
	assertShortRefResolves(t, ctx, req, newAlias, newerRef)
}

func TestShortRefsIdempotentResync(t *testing.T) {
	ctx := context.Background()
	st, req := openShortRefTestRequest(t, ctx)

	refs := []string{"source:msg/1", "source:msg/2", "source:chat/1"}
	records := []ShortRefRecord{
		{Ref: refs[0]},
		{Ref: refs[1]},
		{Ref: refs[2]},
	}
	if _, err := req.AssignShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	aliasesBefore, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	rowsBefore := countShortRefRows(t, ctx, st)

	if _, err := req.AssignShortRefs(ctx, records); err != nil {
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

func TestShortRefsUpdateCanonicalRefInPlace(t *testing.T) {
	ctx := context.Background()
	st, req := openShortRefTestRequest(t, ctx)

	const (
		fullRef         = "legacy:item/1"
		firstCanonical  = "canonical:item/1"
		secondCanonical = "canonical:item/renamed"
	)
	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: fullRef, CanonicalRef: firstCanonical}}); err != nil {
		t.Fatal(err)
	}
	firstAliases, err := req.ShortRefAliases(ctx, []string{firstCanonical})
	if err != nil {
		t.Fatal(err)
	}
	alias := firstAliases[firstCanonical]
	if alias == "" {
		t.Fatalf("first canonical alias missing: %#v", firstAliases)
	}
	rowsBefore := countShortRefRows(t, ctx, st)

	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: fullRef, CanonicalRef: secondCanonical}}); err != nil {
		t.Fatal(err)
	}
	secondAliases, err := req.ShortRefAliases(ctx, []string{secondCanonical})
	if err != nil {
		t.Fatal(err)
	}
	if secondAliases[secondCanonical] != alias {
		t.Fatalf("canonical alias moved: got %q want %q", secondAliases[secondCanonical], alias)
	}
	oldAliases, err := req.ShortRefAliases(ctx, []string{firstCanonical})
	if err != nil {
		t.Fatal(err)
	}
	if oldAliases[firstCanonical] != "" {
		t.Fatalf("old canonical still has alias: %#v", oldAliases)
	}
	assertShortRefResolves(t, ctx, req, alias, secondCanonical)
	if rowsAfter := countShortRefRows(t, ctx, st); rowsAfter != rowsBefore {
		t.Fatalf("short_refs rows changed after canonical update: got %d want %d", rowsAfter, rowsBefore)
	}
}

func TestShortRefsRejectConflictingCanonicalRefs(t *testing.T) {
	ctx := context.Background()
	_, req := openShortRefTestRequest(t, ctx)

	const fullRef = "legacy:item/1"
	records := []ShortRefRecord{
		{Ref: fullRef, CanonicalRef: "canonical:item/1"},
		{Ref: fullRef, CanonicalRef: "canonical:item/renamed"},
	}
	_, err := req.AssignShortRefs(ctx, records)
	if err == nil || !strings.Contains(err.Error(), `short ref "legacy:item/1" has conflicting canonical refs`) {
		t.Fatalf("AssignShortRefs err = %v, want conflicting canonical refs", err)
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

	if assigned, err := req.AssignShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	} else if assigned != refCount {
		t.Fatalf("assigned %d refs, want %d", assigned, refCount)
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
		assertShortRefResolves(t, ctx, req, alias, ref)
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
