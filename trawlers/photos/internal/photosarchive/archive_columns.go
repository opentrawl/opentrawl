package photosarchive

import (
	"context"
	"database/sql"

	"google.golang.org/protobuf/types/known/timestamppb"
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

func optionalArchiveTimeColumn(archiveTime *timestamppb.Timestamp) any {
	if archiveTime == nil {
		return nil
	}
	return formatArchiveTime(archiveTime.AsTime())
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

func optionalArchiveTimeFromColumn(column sql.NullString) (*timestamppb.Timestamp, error) {
	if !column.Valid {
		return nil, nil
	}
	archiveTime, err := parseArchiveTime(column.String)
	if err != nil {
		return nil, err
	}
	return timestamppb.New(archiveTime), nil
}

func optionalInt64Column(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalInt64FromColumn(column sql.NullInt64) *int64 {
	if !column.Valid {
		return nil
	}
	return &column.Int64
}
