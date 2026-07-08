package crawlkit

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/openclaw/crawlkit/shortref"
	ckstore "github.com/openclaw/crawlkit/store"
)

func TestShortRefsPreserveCanonicalAliasSemanticsAtScale(t *testing.T) {
	ctx := context.Background()
	st, err := ckstore.Open(ctx, ckstore.Options{Path: filepath.Join(t.TempDir(), "archive.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

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

	req := &Request{Store: st}
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
