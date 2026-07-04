package cli

import (
	"errors"
	"flag"
	"io"

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
	id, err := store.ParseTweetRef(fs.Arg(0))
	if err != nil {
		return r.contractError("invalid_ref", "ref is not a birdcrawl tweet ref", "Use a ref returned by birdcrawl search --json, such as birdcrawl:tweet/123.")
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		result, err := st.OpenTweet(r.ctx, id)
		if errors.Is(err, store.ErrTweetNotFound) {
			return r.contractError("not_found", "tweet was not found in this archive", "Run birdcrawl search and use one of the returned refs.")
		}
		if err != nil {
			return err
		}
		return r.print(newOpenEnvelope(result))
	})
}
