package cli

import (
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func (r *runtime) runWho(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"who"})
	}
	query := normalizeWhoValue(strings.Join(args, " "))
	if query == "" {
		return usageErr(errors.New("who takes a name or identifier"))
	}
	return r.withArchive(func(st *archive.Store) error {
		result, err := st.ResolveWho(r.ctx, query)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}
