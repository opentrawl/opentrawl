package store

import (
	"context"
	"time"

	"github.com/openclaw/crawlkit/state"
	"github.com/openclaw/wacrawl/internal/store/storedb"
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{DBPath: s.path}
	var err error
	if out.Chats, err = countInt(ctx, s.q.CountChats); err != nil {
		return out, err
	}
	if out.UnreadChats, err = countInt(ctx, s.q.CountUnreadChats); err != nil {
		return out, err
	}
	if out.UnreadMessages, err = countInt(ctx, s.q.CountUnreadMessages); err != nil {
		return out, err
	}
	if out.Contacts, err = countInt(ctx, s.q.CountContacts); err != nil {
		return out, err
	}
	if out.Groups, err = countInt(ctx, s.q.CountGroups); err != nil {
		return out, err
	}
	if out.Participants, err = countInt(ctx, s.q.CountParticipants); err != nil {
		return out, err
	}
	if out.Messages, err = countInt(ctx, s.q.CountMessages); err != nil {
		return out, err
	}
	if out.MediaMessages, err = countInt(ctx, s.q.CountMediaMessages); err != nil {
		return out, err
	}
	bounds, err := s.q.GetMessageTimeBounds(ctx)
	if err != nil {
		return out, err
	}
	out.OldestMessage = fromUnix(bounds.OldestTs)
	out.NewestMessage = fromUnix(bounds.NewestTs)
	// A pre-migration archive (legacy key/value sync_state) errors here; treat
	// it as absent so status still renders, and one sync re-derives it.
	syncState := state.New(s.db)
	if rec, ok, err := getStateAnySource(ctx, syncState, syncEntityType, stateLastImportAt); err == nil && ok {
		if t, terr := time.Parse(time.RFC3339Nano, rec.Value); terr == nil {
			out.LastImportAt = t
		}
	}
	if rec, ok, err := getStateAnySource(ctx, syncState, syncEntityType, stateSourcePath); err == nil && ok {
		out.LastSource = rec.Value
	}
	return out, nil
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

func (s *Store) ListChats(ctx context.Context, limit int) ([]Chat, error) {
	return s.listChats(ctx, ChatFilter{Limit: limit})
}

func (s *Store) ListUnreadChats(ctx context.Context, limit int) ([]Chat, error) {
	return s.listChats(ctx, ChatFilter{Limit: limit, OnlyUnread: true})
}

// CountChats and CountUnreadChats give the list verbs a real total so the
// human output reads "showing X of Y" and truncation is exact, not guessed
// from a full page.
func (s *Store) CountChats(ctx context.Context) (int, error) {
	return countInt(ctx, s.q.CountChats)
}

func (s *Store) CountUnreadChats(ctx context.Context) (int, error) {
	return countInt(ctx, s.q.CountUnreadChats)
}

func (s *Store) listChats(ctx context.Context, filter ChatFilter) ([]Chat, error) {
	// limit <= 0 means everything (the --all contract): SQLite reads LIMIT -1
	// as no limit.
	if filter.Limit <= 0 {
		filter.Limit = -1
	}
	if filter.OnlyUnread {
		rows, err := s.q.ListUnreadChats(ctx, int64(filter.Limit))
		if err != nil {
			return nil, err
		}
		out := make([]Chat, 0, len(rows))
		for _, row := range rows {
			out = append(out, unreadChatFromRow(row))
		}
		return out, nil
	}
	rows, err := s.q.ListChats(ctx, int64(filter.Limit))
	if err != nil {
		return nil, err
	}
	out := make([]Chat, 0, len(rows))
	for _, row := range rows {
		out = append(out, chatFromRow(row))
	}
	return out, nil
}

func countInt(ctx context.Context, count func(context.Context) (int64, error)) (int, error) {
	v, err := count(ctx)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

func chatFromRow(row storedb.ListChatsRow) Chat {
	return Chat{
		JID:            row.Jid,
		Kind:           row.Kind,
		Name:           row.Name,
		LastMessageAt:  fromUnix(row.LastMessageAt),
		UnreadCount:    int(row.UnreadCount),
		Archived:       row.Archived != 0,
		Removed:        row.Removed != 0,
		Hidden:         row.Hidden != 0,
		RawSessionType: int(row.RawSessionType),
		MessageCount:   int(row.MessageCount),
	}
}

func unreadChatFromRow(row storedb.ListUnreadChatsRow) Chat {
	return Chat{
		JID:            row.Jid,
		Kind:           row.Kind,
		Name:           row.Name,
		LastMessageAt:  fromUnix(row.LastMessageAt),
		UnreadCount:    int(row.UnreadCount),
		Archived:       row.Archived != 0,
		Removed:        row.Removed != 0,
		Hidden:         row.Hidden != 0,
		RawSessionType: int(row.RawSessionType),
		MessageCount:   int(row.MessageCount),
	}
}
