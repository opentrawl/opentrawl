package cli

import (
	"errors"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func (r *runtime) runOpen(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"open"})
	}
	if len(args) != 1 {
		return usageErr(errors.New("open takes one ref"))
	}
	return r.withArchive(func(st *archive.Store) error {
		result, err := st.OpenMessage(r.ctx, args[0])
		if err != nil {
			if errors.Is(err, archive.ErrUnknownShortRef) {
				return commandErr("unknown_short_ref", "short ref is unknown", "use a full gogcrawl:msg ref", err)
			}
			if errors.Is(err, archive.ErrAmbiguousShortRef) {
				return commandErr("ambiguous_short_ref", "short ref is ambiguous", "rerun search or use the full gogcrawl:msg ref", err)
			}
			return commandErr("message_not_found", "message could not be opened", "search again and pass a gogcrawl:msg ref", err)
		}
		return r.print(result)
	})
}
