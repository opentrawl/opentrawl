package twitter

import (
	"errors"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

func (r *runtime) loadOpenPost(localShortReference *trawlkit.LocalTrawlerShortReference) (openValue, error) {
	var value openValue
	err := r.withReadOnlyStore(func(st *store.Store) error {
		id, err := r.resolveOpenTweetID(localShortReference)
		if err != nil {
			return err
		}
		result, err := st.OpenTweet(r.ctx, id)
		if errors.Is(err, store.ErrTweetNotFound) {
			return r.contractError("not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPost).Error())
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

func (r *runtime) resolveOpenTweetID(localShortReference *trawlkit.LocalTrawlerShortReference) (string, error) {
	localShortReferenceText := trawlkit.LocalTrawlerShortReferenceText(localShortReference)
	if !trawlkit.ValidShortRef(localShortReferenceText) {
		return "", r.unknownShortRef()
	}
	matches, err := r.req.ResolveShortReference(r.ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", r.unknownShortRef()
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", r.contractError("ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounPost).Error())
	}
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", r.unknownShortRef()
	}
	id, err := store.ParseTweetRef(trawlkit.CanonicalArchiveRecordReferenceText(matches[0]))
	if err != nil {
		return "", err
	}
	return id, nil
}

func (r *runtime) unknownShortRef() error {
	return r.contractError("unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPost).Error())
}
