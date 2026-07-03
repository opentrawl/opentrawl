package cli

import (
	"errors"
	"fmt"
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
		return r.print(result)
	})
}

func boundOpenResult(result archive.OpenResult) archive.OpenResult {
	body, truncated := truncateOpenBody(result.Body)
	result.Body = body
	result.BodyTruncated = truncated
	return result
}

func truncateOpenBody(body string) (string, bool) {
	runes := []rune(body)
	if len(runes) <= maxOpenBodyRunes {
		return body, false
	}
	elided := len(runes) - maxOpenBodyRunes
	return string(runes[:maxOpenBodyRunes]) + "\n\n" + openBodyTruncationMarker(elided), true
}

func openBodyTruncationMarker(elided int) string {
	return fmt.Sprintf("… %s more characters; the full body is in the archive", commaInt(elided))
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
