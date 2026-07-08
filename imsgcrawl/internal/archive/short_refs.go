package archive

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/state"
	"github.com/openclaw/imsgcrawl/internal/messages"
)

const (
	MessageRefPrefix       = "imessage:msg/"
	LegacyMessageRefPrefix = "imsgcrawl:msg/"
)

type ShortRefResolution struct {
	FullRefs []string
	Rebuilt  bool
}

func MessageRef(messageID string) string {
	return MessageRefPrefix + strings.TrimSpace(messageID)
}

func ValidShortRef(alias string) bool {
	return shortref.ValidAlias(strings.TrimSpace(alias))
}

func (s *Store) ResolveShortRef(ctx context.Context, alias string) (ShortRefResolution, error) {
	if s.schemaOutdated {
		return ShortRefResolution{}, ErrSchemaOutdated
	}
	rebuilt, err := s.ensureShortRefs(ctx)
	if err != nil {
		return ShortRefResolution{}, err
	}
	refs, err := shortref.NewSQLiteIndex(s.store.DB()).Lookup(ctx, strings.TrimSpace(alias))
	if err != nil {
		return ShortRefResolution{}, err
	}
	return ShortRefResolution{FullRefs: refs, Rebuilt: rebuilt}, nil
}

func (s *Store) ShortRefForMessage(ctx context.Context, messageID string) (string, error) {
	alias, err := s.shortRefForFullRef(ctx, MessageRef(messageID))
	if err != nil || alias != "" {
		return alias, err
	}
	return s.shortRefForFullRef(ctx, LegacyMessageRefPrefix+strings.TrimSpace(messageID))
}

func (s *Store) shortRefForFullRef(ctx context.Context, fullRef string) (string, error) {
	if s.schemaOutdated {
		return "", ErrSchemaOutdated
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select sr.alias
from short_refs sr
where sr.full_ref = ?
  and not exists (
    select 1
    from short_refs other
    where other.alias = sr.alias
      and other.full_ref <> sr.full_ref
  )
order by length(sr.alias), sr.alias
limit 1
`, fullRef)
	if err != nil {
		return "", nil
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", rows.Err()
	}
	var alias string
	if err := rows.Scan(&alias); err != nil {
		return "", err
	}
	return alias, rows.Err()
}

func (s *Store) ensureShortRefs(ctx context.Context) (bool, error) {
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return false, err
	}
	if current {
		return false, nil
	}
	if err := s.rebuildShortRefs(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) shortRefsCurrent(ctx context.Context) (bool, error) {
	exists, err := tableExists(ctx, s.store.DB(), "short_refs")
	if err != nil || !exists {
		return false, err
	}
	syncState := state.New(s.store.DB())
	lastSync, hasLastSync, err := getStateAnySource(ctx, syncState, syncEntityType, stateLastSyncAt)
	if err != nil {
		return false, err
	}
	builtFor, hasBuiltFor, err := getStateAnySource(ctx, syncState, derivedEntityType, stateShortRefsBuiltFor)
	if err != nil {
		return false, err
	}
	if !hasLastSync || lastSync.Value == "" || !hasBuiltFor || builtFor.Value != lastSync.Value {
		return false, nil
	}
	messageCount, err := countTable(ctx, s.store.DB(), "messages")
	if err != nil {
		return false, err
	}
	var refCount int64
	err = s.store.DB().QueryRowContext(ctx, `select count(distinct full_ref) from short_refs`).Scan(&refCount)
	if err != nil {
		return false, err
	}
	return messageCount == refCount, nil
}

func getStateAnySource(ctx context.Context, syncState *state.Store, entityType, entityID string) (state.Record, bool, error) {
	for _, source := range []string{syncSource, legacySyncSource} {
		rec, ok, err := syncState.Get(ctx, source, entityType, entityID)
		if err != nil || ok {
			return rec, ok, err
		}
	}
	return state.Record{}, false, nil
}

func (s *Store) rebuildShortRefs(ctx context.Context) error {
	rows, err := s.store.DB().QueryContext(ctx, `select source_rowid from messages order by source_rowid`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rec, ok, err := state.New(s.store.DB()).Get(ctx, syncSource, syncEntityType, stateLastSyncAt)
	if err != nil {
		return err
	}
	lastSync := ""
	if ok {
		lastSync = strings.TrimSpace(rec.Value)
	}
	if lastSync == "" {
		lastSync = time.Now().UTC().Format(time.RFC3339)
	}
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		return rebuildShortRefsForIDsInTx(ctx, tx, ids, lastSync)
	})
}

func rebuildShortRefsInTx(ctx context.Context, tx *sql.Tx, rows []messages.Message, syncedAt time.Time) error {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SourceRowID)
	}
	return rebuildShortRefsForIDsInTx(ctx, tx, ids, syncedAt.UTC().Format(time.RFC3339))
}

func rebuildShortRefsForIDsInTx(ctx context.Context, tx *sql.Tx, ids []int64, syncStamp string) error {
	if err := shortref.EnsureSchema(ctx, tx); err != nil {
		return err
	}
	refs := make([]string, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, MessageRef(strconv.FormatInt(id, 10)))
	}
	displayEntries, err := shortref.BuildSlice(refs)
	if err != nil {
		return err
	}
	index := shortref.NewSQLiteIndex(tx)
	if err := index.Clear(ctx); err != nil {
		return err
	}
	if err := index.UpsertEntries(ctx, shortref.LookupEntries(displayEntries)); err != nil {
		return err
	}
	return state.New(tx).Set(ctx, syncSource, derivedEntityType, stateShortRefsBuiltFor, syncStamp)
}
