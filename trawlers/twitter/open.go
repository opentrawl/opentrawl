package twitter

import (
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

func (r *runtime) loadOpenPost(ref string) (openValue, error) {
	var value openValue
	err := r.withReadOnlyStore(func(st *store.Store) error {
		id, err := r.resolveOpenTweetID(ref)
		if err != nil {
			return err
		}
		result, err := st.OpenTweet(r.ctx, id)
		if errors.Is(err, store.ErrTweetNotFound) {
			return r.contractError("not_found", "tweet was not found in this archive")
		}
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		value = openValue{result: result, ownerAuthorID: ownerAuthorID}
		return nil
	})
	return value, err
}

func (r *runtime) resolveOpenTweetID(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		id, err := store.ParseTweetRef(ref)
		if err != nil {
			return "", r.contractError("invalid_ref", "ref is not a twitter tweet ref")
		}
		return id, nil
	}
	if !trawlkit.ValidShortRef(ref) {
		return "", r.unknownShortRef(ref)
	}
	matches, err := r.req.ResolveShortReference(r.ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", r.unknownShortRef(ref)
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", r.contractError("ambiguous_short_ref", "short ref matches more than one tweet")
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
	return r.contractError("unknown_short_ref", fmt.Sprintf("short ref %q was not found", ref))
}
