package messages

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type handleRow struct {
	ID          string
	Service     string
	DisplayName string
	Messages    int64
	LastMessage int64
}

func ExportContacts(ctx context.Context, path string) ([]*person.TrawlerPersonIdentity, error) {
	snap, err := SnapshotPath(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = snap.Close() }()
	st, err := openSnapshot(ctx, snap.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	rows, err := phoneHandleRows(ctx, st.DB())
	if err != nil {
		return nil, err
	}
	byPhone := map[string]handleRow{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		phoneKey := NormalizePhone(row.ID)
		if phoneKey == "" || !LooksPhoneLike(row.ID) {
			continue
		}
		if current, ok := byPhone[phoneKey]; ok {
			if preferHandle(row, current) {
				byPhone[phoneKey] = row
			}
			continue
		}
		byPhone[phoneKey] = row
		order = append(order, phoneKey)
	}
	sort.SliceStable(order, func(i, j int) bool {
		left := byPhone[order[i]]
		right := byPhone[order[j]]
		if left.LastMessage != right.LastMessage {
			return left.LastMessage > right.LastMessage
		}
		return order[i] < order[j]
	})
	out := make([]*person.TrawlerPersonIdentity, 0, len(order))
	for _, key := range order {
		row := byPhone[key]
		name := strings.TrimSpace(row.DisplayName)
		if name == "" {
			name = strings.TrimSpace(row.ID)
		}
		if name == "" {
			continue
		}
		out = append(out, &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive: &person.PersonIdentifierWithinTrawlerArchive{
				PersonIdentifierWithinTrawlerArchive: "phone:" + key,
			},
			PersonDisplayName:  name,
			PersonPhoneNumbers: []string{strings.TrimSpace(row.ID)},
		})
	}
	return out, nil
}

func openSnapshot(ctx context.Context, path string) (*store.Store, error) {
	st, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := requireMessagesTables(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

func requireMessagesTables(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"handle", "chat", "chat_handle_join", "message"} {
		var name string
		err := db.QueryRowContext(ctx, tableExistsSQL, table).Scan(&name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("messages database is missing table " + table)
			}
			return err
		}
	}
	return nil
}

func phoneHandleRows(ctx context.Context, db *sql.DB) ([]handleRow, error) {
	rows, err := db.QueryContext(ctx, phoneHandleRowsSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []handleRow
	for rows.Next() {
		var row handleRow
		if err := rows.Scan(&row.ID, &row.Service, &row.DisplayName, &row.Messages, &row.LastMessage); err != nil {
			return nil, err
		}
		if !LooksPhoneLike(row.ID) {
			continue
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func preferHandle(candidate, current handleRow) bool {
	if candidate.LastMessage != current.LastMessage {
		return candidate.LastMessage > current.LastMessage
	}
	if candidate.Messages != current.Messages {
		return candidate.Messages > current.Messages
	}
	if candidate.DisplayName != "" && current.DisplayName == "" {
		return true
	}
	return len([]rune(candidate.DisplayName)) > len([]rune(current.DisplayName))
}

func NormalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return strings.TrimPrefix(b.String(), "00")
}

func LooksPhoneLike(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	hasDigit := false
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+', r == ' ', r == '\t', r == '(', r == ')', r == '-', r == '.':
			continue
		default:
			return false
		}
	}
	return hasDigit
}
