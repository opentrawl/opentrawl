package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

const twitterIndexBatchSize = 5000

// ExportArchiveIndexChanges streams tweets (with observed tweet_roles) and X
// direct messages incrementally into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxTweetRow, tweetCount, maxDMRow, dmCount, roleCount int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(rowid), 0), count(*) from tweets`).Scan(&maxTweetRow, &tweetCount); err != nil {
		return fmt.Errorf("read twitter tweet cursor state: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(rowid), 0), count(*) from dm_messages`).Scan(&maxDMRow, &dmCount); err != nil {
		return fmt.Errorf("read twitter dm cursor state: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from tweet_roles`).Scan(&roleCount); err != nil {
		return fmt.Errorf("read twitter role cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("twitter:%d:%d:%d:%d:%d", maxTweetRow, tweetCount, maxDMRow, dmCount, roleCount)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevTweetRow, prevTweetCount, prevDMRow, prevDMCount, prevRoleCount, ok := parseTwitterCursor(sinceOpaqueCursor)
	incremental := ok && tweetCount >= prevTweetCount && dmCount >= prevDMCount && roleCount >= prevRoleCount && maxTweetRow >= prevTweetRow && maxDMRow >= prevDMRow

	startTweetRow, startDMRow := int64(0), int64(0)
	if incremental {
		startTweetRow = prevTweetRow
		startDMRow = prevDMRow
	}

	rolesByTweet, err := s.loadObservedTweetRoles(ctx)
	if err != nil {
		return err
	}
	dmConvRefs, err := s.loadTwitterDMConversationShortRefs(ctx)
	if err != nil {
		return err
	}

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, twitterIndexBatchSize)
	shortRefOnly := dmConvRefs
	firstBatch := true
	flush := func(isFinal bool) error {
		batch := &indexcontract.ArchiveIndexChangeBatch{
			Upserts:                     upserts,
			ShortReferenceOnlyEntries:   shortRefOnly,
			CompleteSnapshotReplacement: !incremental && firstBatch,
		}
		firstBatch = false
		if isFinal {
			batch.NextOpaqueCursor = nextCursor
		}
		if err := emit(batch); err != nil {
			return err
		}
		if isFinal {
			upserts = nil
		} else {
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, twitterIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	if err := s.streamTwitterTweets(ctx, startTweetRow, rolesByTweet, &upserts, func() error {
		if len(upserts)+len(shortRefOnly) >= twitterIndexBatchSize {
			return flush(false)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := s.streamTwitterDMs(ctx, startDMRow, &upserts, &shortRefOnly, func() error {
		if len(upserts)+len(shortRefOnly) >= twitterIndexBatchSize {
			return flush(false)
		}
		return nil
	}); err != nil {
		return err
	}
	return flush(true)
}

func (s *Store) streamTwitterTweets(
	ctx context.Context,
	startRow int64,
	rolesByTweet map[string]string,
	upserts *[]*indexcontract.IndexedArchiveRecordUpsert,
	maybeFlush func() error,
) error {
	rows, err := s.db.QueryContext(ctx, `
		select id, created_at, coalesce(author_handle, ''), coalesce(author_name, ''), text, coalesce(conversation_id, '')
		from tweets
		where rowid > ? and trim(id) <> ''
		order by rowid
	`, startRow)
	if err != nil {
		return fmt.Errorf("read tweets for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, createdAt, handle, authorName, text, convID string
		if err := rows.Scan(&id, &createdAt, &handle, &authorName, &text, &convID); err != nil {
			return fmt.Errorf("scan tweet for unified index: %w", err)
		}
		roleText := rolesByTweet[id]
		canonical := trawlkit.NewCanonicalArchiveRecordReference(TweetRef(id))
		convKey := "twitter:conv/" + strings.TrimSpace(convID)
		if strings.TrimSpace(convID) == "" {
			convKey = TweetRef(id)
		}
		occUnix, hasTime := parseTwitterTime(createdAt)
		var participants []*indexcontract.ArchiveRecordParticipant
		if strings.TrimSpace(authorName) != "" || strings.TrimSpace(handle) != "" {
			addr := strings.TrimSpace(handle)
			if addr != "" && !strings.HasPrefix(addr, "@") {
				addr = "@" + addr
			}
			participants = []*indexcontract.ArchiveRecordParticipant{{
				DisplayName:     strings.TrimSpace(authorName),
				AddressOrHandle: addr,
				ParticipantRole: "author",
			}}
		}
		*upserts = append(*upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_SOCIAL_POST,
			ObservedSourceRole:             mapTwitterRole(roleText),
			ConversationOrThreadIdentifier: convKey,
			DisplayContextTitle:            strings.TrimSpace(roleText),
			OccurredUnixSeconds:            occUnix,
			HasOccurredTime:                hasTime,
			HasTimeOfDay:                   true,
			BodyText:                       strings.TrimSpace(text),
			Participants:                   participants,
		})
		if err := maybeFlush(); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) streamTwitterDMs(
	ctx context.Context,
	startRow int64,
	upserts *[]*indexcontract.IndexedArchiveRecordUpsert,
	shortRefOnly *[]*indexcontract.ShortReferenceOnlyRegistrationEntry,
	maybeFlush func() error,
) error {
	rows, err := s.db.QueryContext(ctx, `
		select id, coalesce(conversation_id, ''), created_at, coalesce(text, '')
		from dm_messages
		where rowid > ? and trim(id) <> ''
		order by rowid
	`, startRow)
	if err != nil {
		return fmt.Errorf("read twitter dms for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, convID, createdAt, text string
		if err := rows.Scan(&id, &convID, &createdAt, &text); err != nil {
			return fmt.Errorf("scan twitter dm for unified index: %w", err)
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(DMMessageRef(id))
		if strings.TrimSpace(text) == "" {
			*shortRefOnly = append(*shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		}
		occUnix, hasTime := parseTwitterTime(createdAt)
		*upserts = append(*upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CHAT_MESSAGE,
			ObservedSourceRole:             indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_DIRECT_MESSAGE,
			ConversationOrThreadIdentifier: DMConversationRef(convID),
			DisplayContextTitle:            "dm",
			OccurredUnixSeconds:            occUnix,
			HasOccurredTime:                hasTime,
			HasTimeOfDay:                   true,
			BodyText:                       strings.TrimSpace(text),
		})
		if err := maybeFlush(); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) loadObservedTweetRoles(ctx context.Context) (map[string]string, error) {
	priority := map[string]int{
		"authored":      5,
		"bookmark":      4,
		"mention":       3,
		"like":          2,
		"reply_context": 1,
	}
	rows, err := s.db.QueryContext(ctx, `select tweet_id, role from tweet_roles`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string)
	for rows.Next() {
		var tweetID, role string
		if err := rows.Scan(&tweetID, &role); err != nil {
			return nil, err
		}
		if existing, ok := out[tweetID]; !ok || priority[role] > priority[existing] {
			out[tweetID] = role
		}
	}
	return out, rows.Err()
}

func (s *Store) loadTwitterDMConversationShortRefs(ctx context.Context) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := s.db.QueryContext(ctx, `select id from dm_conversations order by id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if strings.TrimSpace(id) == "" {
			continue
		}
		ref := trawlkit.NewCanonicalArchiveRecordReference(DMConversationRef(id))
		out = append(out, &indexcontract.ShortReferenceOnlyRegistrationEntry{
			StableRecordReference:   ref,
			ResolvedRecordReference: ref,
		})
	}
	return out, rows.Err()
}

func mapTwitterRole(role string) indexcontract.ObservedArchiveRecordRole {
	switch role {
	case "authored":
		return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_AUTHORED
	case "bookmark":
		return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_BOOKMARK
	case "mention":
		return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_MENTION
	case "like":
		return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_LIKE
	case "reply_context":
		return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_REPLY_CONTEXT
	default:
		return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_OTHER
	}
}

func parseTwitterTime(raw string) (int64, bool) {
	trimmed := strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

func parseTwitterCursor(cursor string) (int64, int64, int64, int64, int64, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 6 || parts[0] != "twitter" {
		return 0, 0, 0, 0, 0, false
	}
	a, e1 := strconv.ParseInt(parts[1], 10, 64)
	b, e2 := strconv.ParseInt(parts[2], 10, 64)
	c, e3 := strconv.ParseInt(parts[3], 10, 64)
	d, e4 := strconv.ParseInt(parts[4], 10, 64)
	e, e5 := strconv.ParseInt(parts[5], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		return 0, 0, 0, 0, 0, false
	}
	return a, b, c, d, e, true
}
