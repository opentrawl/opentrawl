package birdcrawl

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func (r *runtime) runOpen(args []string) error {
	fs := flag.NewFlagSet("birdcrawl open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 1 {
		return usageErr(errors.New("open takes exactly one ref"))
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		id, err := r.resolveOpenTweetID(st, fs.Arg(0))
		if err != nil {
			return err
		}
		result, err := st.OpenTweet(r.ctx, id)
		if errors.Is(err, store.ErrTweetNotFound) {
			return r.contractError("not_found", "tweet was not found in this archive", "Run trawl birdcrawl search and use one of the returned refs.")
		}
		if err != nil {
			return err
		}
		aliases, err := aliasesForOpen(r.ctx, st, result)
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		return r.print(newOpenEnvelope(result, aliases, ownerAuthorID))
	})
}

func (r *runtime) resolveOpenTweetID(st *store.Store, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		id, err := store.ParseTweetRef(ref)
		if err != nil {
			return "", r.contractError("invalid_ref", "ref is not a birdcrawl tweet ref", "Use a ref returned by trawl birdcrawl search --json, such as birdcrawl:tweet/123.")
		}
		return id, nil
	}
	if !shortref.ValidAlias(ref) {
		return "", r.unknownShortRef(ref)
	}
	matches, err := st.ResolveShortRef(r.ctx, ref)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", r.unknownShortRef(ref)
	case 1:
		id, err := store.ParseTweetRef(matches[0])
		if err != nil {
			return "", err
		}
		return id, nil
	default:
		return "", r.contractError("ambiguous_short_ref", "short ref matches more than one tweet", "Rerun trawl birdcrawl search or use the full ref.")
	}
}

func (r *runtime) unknownShortRef(ref string) error {
	return r.contractError("unknown_short_ref", fmt.Sprintf("short ref %q was not found", ref), "re-run the listing to get a fresh ref, or use the full ref from --json output")
}
