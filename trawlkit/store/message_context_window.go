package store

import (
	"context"
	"fmt"
	"slices"
)

// SurroundingMessageContextWindow holds a target chat message together with
// symmetric earlier and later neighbour slices read from the same conversation.
type SurroundingMessageContextWindow[Message any] struct {
	Before          []Message
	Target          Message
	After           []Message
	BeforeTruncated bool
	AfterTruncated  bool
}

// MessagesInChronologicalOrder returns Before (oldest to newest), Target, and
// After (oldest to newest) as one slice.
func (w SurroundingMessageContextWindow[Message]) MessagesInChronologicalOrder() []Message {
	out := make([]Message, 0, len(w.Before)+1+len(w.After))
	out = append(out, w.Before...)
	out = append(out, w.Target)
	out = append(out, w.After...)
	return out
}

// MessagesNewestFirst returns After (newest to oldest), Target, and Before
// (newest to oldest) as one slice.
func (w SurroundingMessageContextWindow[Message]) MessagesNewestFirst() []Message {
	out := make([]Message, 0, len(w.After)+1+len(w.Before))
	for i := len(w.After) - 1; i >= 0; i-- {
		out = append(out, w.After[i])
	}
	out = append(out, w.Target)
	for i := len(w.Before) - 1; i >= 0; i-- {
		out = append(out, w.Before[i])
	}
	return out
}

// ReadSurroundingMessageContextWindow fetches up to eachSideRadius earlier
// messages and up to eachSideRadius later messages around target using the
// standard +1 over-fetch truncation check.
//
// readEarlierNewestFirst must return messages strictly earlier than target
// ordered newest-first (closest to target first) up to limit rows.
// readLaterOldestFirst must return messages strictly later than target ordered
// oldest-first (closest to target first) up to limit rows.
func ReadSurroundingMessageContextWindow[Message any](
	ctx context.Context,
	target Message,
	eachSideRadius int,
	readEarlierNewestFirst func(ctx context.Context, limit int) ([]Message, error),
	readLaterOldestFirst func(ctx context.Context, limit int) ([]Message, error),
) (SurroundingMessageContextWindow[Message], error) {
	if eachSideRadius < 0 {
		eachSideRadius = 0
	}
	window := SurroundingMessageContextWindow[Message]{Target: target}
	if eachSideRadius == 0 {
		return window, nil
	}

	earlier, err := readEarlierNewestFirst(ctx, eachSideRadius+1)
	if err != nil {
		return SurroundingMessageContextWindow[Message]{}, err
	}
	window.BeforeTruncated = len(earlier) > eachSideRadius
	if window.BeforeTruncated {
		earlier = earlier[:eachSideRadius]
	}
	slices.Reverse(earlier)
	window.Before = earlier

	later, err := readLaterOldestFirst(ctx, eachSideRadius+1)
	if err != nil {
		return SurroundingMessageContextWindow[Message]{}, err
	}
	window.AfterTruncated = len(later) > eachSideRadius
	if window.AfterTruncated {
		later = later[:eachSideRadius]
	}
	window.After = later

	return window, nil
}

// SQLKeysetBeforeAfterClause builds the shared SQL keyset predicate and ORDER BY
// clause for reading messages strictly before (earlier=true, newest-first) or
// strictly after (earlier=false, oldest-first) a target (timeColumn, tieBreakerColumn).
func SQLKeysetBeforeAfterClause(
	timeColumn string,
	tieBreakerColumn string,
	earlier bool,
) (wherePredicate string, orderByClause string) {
	if earlier {
		return fmt.Sprintf(
				"(%s < ? or (%s = ? and %s < ?))",
				timeColumn,
				timeColumn,
				tieBreakerColumn,
			),
			fmt.Sprintf("%s desc, %s desc", timeColumn, tieBreakerColumn)
	}
	return fmt.Sprintf(
			"(%s > ? or (%s = ? and %s > ?))",
			timeColumn,
			timeColumn,
			tieBreakerColumn,
		),
		fmt.Sprintf("%s asc, %s asc", timeColumn, tieBreakerColumn)
}
