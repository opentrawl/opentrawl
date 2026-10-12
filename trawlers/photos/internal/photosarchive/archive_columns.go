package photosarchive

import (
	"context"
	"database/sql"
)

type archiveQueryer interface {
	QueryContext(ctx context.Context, query string, arguments ...any) (*sql.Rows, error)
}

// Missing values stay missing in the archive: unset optional fields and empty
// text are stored as null, never as zero or an empty string.

func optionalBoolColumn(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalInt32Column(value *int32) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalFloat64Column(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func emptyTextAsNull(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func nullWhenMessageAbsent(messageIsPresent bool, value any) any {
	if !messageIsPresent {
		return nil
	}
	return value
}

func optionalBoolFromColumn(column sql.NullBool) *bool {
	if !column.Valid {
		return nil
	}
	return &column.Bool
}

func optionalInt32FromColumn(column sql.NullInt32) *int32 {
	if !column.Valid {
		return nil
	}
	return &column.Int32
}

func optionalFloat64FromColumn(column sql.NullFloat64) *float64 {
	if !column.Valid {
		return nil
	}
	return &column.Float64
}
