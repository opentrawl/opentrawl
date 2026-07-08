package birdcrawl

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func (r *runtime) runOpen(args []string) error {
	fs := flag.NewFlagSet("twitter open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 1 {
		return usageErr(errors.New("open takes exactly one ref"))
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		id, err := r.resolveOpenTweetID(fs.Arg(0))
		if err != nil {
			return err
		}
		result, err := st.OpenTweet(r.ctx, id)
		if errors.Is(err, store.ErrTweetNotFound) {
			return r.contractError("not_found", "tweet was not found in this archive", "run trawl twitter search and use one of the returned refs.")
		}
		if err != nil {
			return err
		}
		aliases, err := aliasesForOpen(r.ctx, r.req, result)
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

func (r *runtime) resolveOpenTweetID(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		id, err := store.ParseTweetRef(ref)
		if err != nil {
			return "", r.contractError("invalid_ref", "ref is not a twitter tweet ref", "Use a ref returned by trawl twitter search --json, such as twitter:tweet/123.")
		}
		return id, nil
	}
	if !crawlkit.ValidShortRef(ref) {
		return "", r.unknownShortRef(ref)
	}
	matches, err := r.req.ResolveShortRef(r.ctx, ref)
	if errors.Is(err, crawlkit.ErrUnknownShortRef) {
		return "", r.unknownShortRef(ref)
	}
	if errors.Is(err, crawlkit.ErrAmbiguousShortRef) {
		return "", r.contractError("ambiguous_short_ref", "short ref matches more than one tweet", "Rerun trawl twitter search or use the full ref.")
	}
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", r.unknownShortRef(ref)
	}
	id, err := store.ParseTweetRef(matches[0])
	if err != nil {
		return "", err
	}
	return id, nil
}

func (r *runtime) unknownShortRef(ref string) error {
	return r.contractError("unknown_short_ref", fmt.Sprintf("short ref %q was not found", ref), "re-run the listing to get a fresh ref, or use the full ref from --json output")
}
