package cli

import (
	"errors"
	"strconv"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

const maxOpenBodyRunes = 4000

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
		result = boundOpenResult(result)
		return r.print(openOutput{OpenResult: result, shortRef: openShortRef(r, st, result.Ref)})
	})
}

// openOutput pairs the frozen open envelope with the message's short
// alias for the human card; full machine refs stay in JSON
// (docs/rendering.md). The alias is unexported so JSON output stays the
// bare OpenResult.
type openOutput struct {
	archive.OpenResult
	shortRef string
}

func openShortRef(r *runtime, st *archive.Store, ref string) string {
	aliases, err := st.ShortRefs(r.ctx, []string{ref})
	if err != nil {
		return ""
	}
	return aliases[ref]
}

func boundOpenResult(result archive.OpenResult) archive.OpenResult {
	body, elided := truncateOpenBody(result.Body)
	result.Body = body
	result.BodyTruncated = elided > 0
	result.BodyElidedChars = elided
	return result
}

// truncateOpenBody caps the body at maxOpenBodyRunes and reports how many
// characters were cut. The body it returns carries only mail content — the
// truncation hint is a separate line the human renderer adds, never text
// stuffed into the data.
func truncateOpenBody(body string) (string, int) {
	runes := []rune(body)
	if len(runes) <= maxOpenBodyRunes {
		return body, 0
	}
	return string(runes[:maxOpenBodyRunes]), len(runes) - maxOpenBodyRunes
}

func commaInt(value int) string {
	raw := strconv.Itoa(value)
	if len(raw) <= 3 {
		return raw
	}
	head := len(raw) % 3
	if head == 0 {
		head = 3
	}
	out := raw[:head]
	for i := head; i < len(raw); i += 3 {
		out += "," + raw[i:i+3]
	}
	return out
}
