package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/shortref"
)

const shortRefFingerprintKind = "short_refs_fingerprint"

func (s *Store) EnsureShortRefs(ctx context.Context) error {
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return err
	}
	if current {
		return nil
	}
	if err := s.RebuildShortRefs(ctx); err != nil {
		return err
	}
	return s.logShortRefRefresh()
}

func (s *Store) RebuildShortRefs(ctx context.Context) error {
	refs, err := s.allTweetFullRefs(ctx)
	if err != nil {
		return err
	}
	return s.base.WithTx(ctx, func(tx *sql.Tx) error {
		index := shortref.NewSQLiteIndex(tx)
		if err := shortref.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := index.Clear(ctx); err != nil {
			return err
		}
		entries, err := shortref.BuildSlice(refs)
		if err != nil {
			return err
		}
		if err := index.UpsertEntries(ctx, shortref.LookupEntries(entries)); err != nil {
			return err
		}
		return upsertSyncState(ctx, tx, SyncStateUpdate{
			Kind:       shortRefFingerprintKind,
			Cursor:     shortRefsFingerprint(refs),
			LastSyncAt: time.Now().UTC(),
			LastResult: "ok",
		})
	})
}

func (s *Store) ResolveShortRef(ctx context.Context, alias string) ([]string, error) {
	alias = strings.TrimSpace(alias)
	if !shortref.ValidAlias(alias) {
		return nil, nil
	}
	if err := s.EnsureShortRefs(ctx); err != nil {
		return nil, err
	}
	return shortref.NewSQLiteIndex(s.db).Lookup(ctx, alias)
}

func (s *Store) ShortRefAliases(ctx context.Context, fullRefs []string) (map[string]string, error) {
	if len(fullRefs) == 0 {
		return nil, nil
	}
	if err := s.EnsureShortRefs(ctx); err != nil {
		return nil, err
	}
	return shortref.NewSQLiteIndex(s.db).Aliases(ctx, fullRefs)
}

func (s *Store) shortRefsCurrent(ctx context.Context) (bool, error) {
	fingerprint, err := s.SyncState(ctx, shortRefFingerprintKind)
	if err != nil {
		return false, err
	}
	stored := fingerprint.Cursor
	if stored == "" {
		return false, nil
	}
	refs, err := s.allTweetFullRefs(ctx)
	if err != nil {
		return false, err
	}
	if stored != shortRefsFingerprint(refs) {
		return false, nil
	}
	if _, err := s.shortRefFullRefs(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) logShortRefRefresh() error {
	if s.log == nil {
		return nil
	}
	return s.log.Info("short_refs_refresh", "rebuilt derived short ref cache on read")
}

func (s *Store) allTweetFullRefs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `select id from tweets where trim(id) <> '' order by id`)
	if err != nil {
		return nil, fmt.Errorf("read tweet refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tweet ref: %w", err)
		}
		refs = append(refs, TweetRef(id))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read tweet refs: %w", err)
	}
	return refs, nil
}

func (s *Store) shortRefFullRefs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `select distinct full_ref from short_refs order by full_ref`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func tweetFullRefs(tweets []Tweet) []string {
	refs := make([]string, 0, len(tweets))
	for _, tweet := range tweets {
		id := strings.TrimSpace(tweet.ID)
		if id != "" {
			refs = append(refs, TweetRef(id))
		}
	}
	sort.Strings(refs)
	return refs
}

func shortRefsFingerprint(refs []string) string {
	hash := sha256.New()
	for _, ref := range refs {
		_, _ = hash.Write([]byte(ref))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
