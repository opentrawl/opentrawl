package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/shortref"
)

const shortRefFingerprintKey = "short_refs_fingerprint"

var ErrShortRefIndexStale = errors.New("short ref index is stale")

func (s *Store) EnsureShortRefs(ctx context.Context) error {
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return err
	}
	if current {
		return nil
	}
	return s.RebuildShortRefs(ctx)
}

func (s *Store) RebuildShortRefs(ctx context.Context) error {
	refs, err := s.allMessageFullRefs(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
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
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
insert into sync_state(key, value, updated_at)
values(?, ?, ?)
on conflict(key) do update set value = excluded.value, updated_at = excluded.updated_at
`, shortRefFingerprintKey, shortRefsFingerprint(refs), unix(now)); err != nil {
		return fmt.Errorf("record short ref fingerprint: %w", err)
	}
	return tx.Commit()
}

func (s *Store) ResolveShortRef(ctx context.Context, alias string) ([]string, error) {
	alias = strings.TrimSpace(alias)
	if !shortref.ValidAlias(alias) {
		return nil, nil
	}
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, ErrShortRefIndexStale
	}
	return shortref.NewSQLiteIndex(s.db).Lookup(ctx, alias)
}

func (s *Store) ShortRefAliases(ctx context.Context, fullRefs []string) (map[string]string, error) {
	if len(fullRefs) == 0 {
		return nil, nil
	}
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, ErrShortRefIndexStale
	}
	args := make([]any, 0, len(fullRefs))
	for _, fullRef := range fullRefs {
		args = append(args, fullRef)
	}
	rows, err := s.db.QueryContext(ctx, `
select full_ref, alias
from short_refs
where full_ref in (`+queryPlaceholders(len(args))+`)
order by full_ref, length(alias) desc
`, args...)
	if err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	aliases := make(map[string]string, len(fullRefs))
	for rows.Next() {
		var fullRef, alias string
		if err := rows.Scan(&fullRef, &alias); err != nil {
			return nil, fmt.Errorf("scan short ref alias: %w", err)
		}
		if aliases[fullRef] == "" {
			aliases[fullRef] = alias
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	return aliases, nil
}

func (s *Store) shortRefsCurrent(ctx context.Context) (bool, error) {
	stored, err := s.q.GetSyncState(ctx, shortRefFingerprintKey)
	if err != nil {
		return false, nil
	}
	refs, err := s.allMessageFullRefs(ctx)
	if err != nil {
		return false, err
	}
	if stored != shortRefsFingerprint(refs) {
		return false, nil
	}
	if len(refs) == 0 {
		return true, nil
	}
	indexedRefs, err := s.shortRefFullRefs(ctx)
	if err != nil {
		return false, nil
	}
	if len(indexedRefs) != len(refs) {
		return false, nil
	}
	for i := range refs {
		if refs[i] != indexedRefs[i] {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) allMessageFullRefs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `select msg_id from messages where trim(msg_id) <> '' order by msg_id`)
	if err != nil {
		return nil, fmt.Errorf("read message refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref: %w", err)
		}
		refs = append(refs, MessageRefPrefix+id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func messageFullRefs(messages []Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		id := strings.TrimSpace(message.MessageID)
		if id != "" {
			refs = append(refs, MessageRefPrefix+id)
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
