package unifiedindex

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
)

const UnifiedIndexDatabaseFilename = "index.db"

func ResolveIndexDatabasePath(stateRoot string) string {
	return filepath.Join(stateRoot, UnifiedIndexDatabaseFilename)
}

const unifiedIndexSchemaSQL = `
CREATE TABLE IF NOT EXISTS indexed_trawler_state (
	trawler                TEXT PRIMARY KEY,
	trawler_display_name   TEXT NOT NULL DEFAULT '',
	shortrefs_migrated     INTEGER NOT NULL DEFAULT 0,
	index_cursor           TEXT NOT NULL DEFAULT '',
	indexed_at_utc         TEXT NOT NULL DEFAULT '',
	record_count           INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS indexed_archive_records (
	doc_id                 INTEGER PRIMARY KEY,
	trawler                TEXT NOT NULL,
	trawler_display_name   TEXT NOT NULL,
	record_kind            TEXT NOT NULL,
	source_role            TEXT NOT NULL DEFAULT '',
	canonical_ref          TEXT NOT NULL,
	resolved_ref           TEXT NOT NULL,
	record_anchor          TEXT NOT NULL DEFAULT '',
	conversation_key       TEXT NOT NULL DEFAULT '',
	display_context        TEXT NOT NULL DEFAULT '',
	occurred_unix          INTEGER,
	has_time_of_day        INTEGER NOT NULL DEFAULT 1,
	utc_offset_seconds     INTEGER NOT NULL DEFAULT 0,
	end_unix               INTEGER,
	title                  TEXT NOT NULL DEFAULT '',
	people_text            TEXT NOT NULL DEFAULT '',
	body_text              TEXT NOT NULL DEFAULT '',
	meta_text              TEXT NOT NULL DEFAULT '',
	observed_labels_text   TEXT NOT NULL DEFAULT '',
	status_labels_text     TEXT NOT NULL DEFAULT '',
	money_decimal_amount   TEXT NOT NULL DEFAULT '',
	money_currency_code    TEXT NOT NULL DEFAULT '',
	money_is_pending       INTEGER NOT NULL DEFAULT 0,
	UNIQUE(trawler, canonical_ref)
);

CREATE VIRTUAL TABLE IF NOT EXISTS indexed_archive_records_fts USING fts5(
	title,
	people_text,
	body_text,
	meta_text,
	observed_labels_text,
	content='indexed_archive_records',
	content_rowid='doc_id',
	tokenize='porter unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS trg_indexed_archive_records_ai
AFTER INSERT ON indexed_archive_records BEGIN
	INSERT INTO indexed_archive_records_fts(rowid, title, people_text, body_text, meta_text, observed_labels_text)
	VALUES (new.doc_id, new.title, new.people_text, new.body_text, new.meta_text, new.observed_labels_text);
END;

CREATE TRIGGER IF NOT EXISTS trg_indexed_archive_records_ad
AFTER DELETE ON indexed_archive_records BEGIN
	INSERT INTO indexed_archive_records_fts(indexed_archive_records_fts, rowid, title, people_text, body_text, meta_text, observed_labels_text)
	VALUES ('delete', old.doc_id, old.title, old.people_text, old.body_text, old.meta_text, old.observed_labels_text);
END;

CREATE TRIGGER IF NOT EXISTS trg_indexed_archive_records_au
AFTER UPDATE ON indexed_archive_records BEGIN
	INSERT INTO indexed_archive_records_fts(indexed_archive_records_fts, rowid, title, people_text, body_text, meta_text, observed_labels_text)
	VALUES ('delete', old.doc_id, old.title, old.people_text, old.body_text, old.meta_text, old.observed_labels_text);
	INSERT INTO indexed_archive_records_fts(rowid, title, people_text, body_text, meta_text, observed_labels_text)
	VALUES (new.doc_id, new.title, new.people_text, new.body_text, new.meta_text, new.observed_labels_text);
END;

CREATE TABLE IF NOT EXISTS indexed_record_anchors (
	doc_id       INTEGER NOT NULL,
	anchor_kind  TEXT NOT NULL,
	anchor_value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS indexed_money_movements (
	doc_id                 INTEGER PRIMARY KEY,
	trawler                TEXT NOT NULL,
	canonical_ref          TEXT NOT NULL,
	booking_date           TEXT NOT NULL,
	occurred_unix          INTEGER,
	signed_minor_units     INTEGER,
	abs_minor_units        INTEGER,
	currency_code          TEXT NOT NULL,
	orig_abs_minor_units   INTEGER,
	orig_currency_code     TEXT NOT NULL DEFAULT '',
	counterparty           TEXT NOT NULL DEFAULT '',
	description            TEXT NOT NULL DEFAULT '',
	merchant_city          TEXT NOT NULL DEFAULT '',
	merchant_country       TEXT NOT NULL DEFAULT '',
	pending                INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS indexed_time_windows (
	window_id              TEXT PRIMARY KEY,
	trawler                TEXT NOT NULL,
	window_kind            TEXT NOT NULL,
	place_title            TEXT NOT NULL,
	date_title             TEXT NOT NULL,
	start_unix             INTEGER NOT NULL,
	end_unix               INTEGER NOT NULL,
	related_canonical_ref  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS indexed_archive_metadata (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS indexed_trawler_monthly_counts (
	trawler        TEXT NOT NULL,
	calendar_year  INTEGER NOT NULL,
	calendar_month INTEGER NOT NULL,
	record_count   INTEGER NOT NULL,
	PRIMARY KEY (trawler, calendar_year, calendar_month)
);

CREATE TABLE IF NOT EXISTS indexed_trawler_history_summary (
	trawler              TEXT PRIMARY KEY,
	oldest_occurred_unix INTEGER,
	undated_record_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_records_trawler_time ON indexed_archive_records(trawler, occurred_unix DESC);
CREATE INDEX IF NOT EXISTS idx_records_time ON indexed_archive_records(occurred_unix DESC);
CREATE INDEX IF NOT EXISTS idx_records_conversation ON indexed_archive_records(trawler, conversation_key);
CREATE INDEX IF NOT EXISTS idx_records_resolved_ref ON indexed_archive_records(trawler, resolved_ref);
CREATE INDEX IF NOT EXISTS idx_anchors_kind_value ON indexed_record_anchors(anchor_kind, anchor_value);
CREATE INDEX IF NOT EXISTS idx_anchors_doc ON indexed_record_anchors(doc_id);
CREATE INDEX IF NOT EXISTS idx_money_time ON indexed_money_movements(occurred_unix DESC);
CREATE INDEX IF NOT EXISTS idx_money_abs_amount ON indexed_money_movements(abs_minor_units);
CREATE INDEX IF NOT EXISTS idx_windows_time ON indexed_time_windows(start_unix, end_unix);
`

func EnsureUnifiedIndexSchema(ctx context.Context, db *sql.DB) error {
	if err := shortref.EnsureScopedSchema(ctx, db); err != nil {
		return fmt.Errorf("ensure unified short ref schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, unifiedIndexSchemaSQL); err != nil {
		return fmt.Errorf("ensure unified index schema: %w", err)
	}
	if err := backfillMissingTrawlerMonthlyHistories(ctx, db); err != nil {
		return fmt.Errorf("backfill unified index monthly histories: %w", err)
	}
	return nil
}

// IndexedTrawlerStatus summarizes one trawler's current state inside index.db.
type IndexedTrawlerStatus struct {
	TrawlerIdentity    string
	TrawlerDisplayName string
	RecordCount        int64
	IndexCursor        string
	IndexedAtUTC       time.Time
}

// UnifiedIndexStatus summarizes the disposable core index database.
type UnifiedIndexStatus struct {
	IndexPath          string
	Exists             bool
	UpdatedAtUTC       time.Time
	TotalIndexedCount  int64
	AnchorCount        int64
	MoneyMovementCount int64
	TimeWindowCount    int64
	DatabaseByteCount  uint64
	Trawlers           []IndexedTrawlerStatus
}

// UnifiedIndexRebuildReport summarizes a core-driven index rebuild across trawlers.
type UnifiedIndexRebuildReport struct {
	IndexPath          string
	BuiltAtUTC         time.Time
	TotalIndexedCount  int64
	AnchorCount        int64
	MoneyMovementCount int64
	TimeWindowCount    int64
	DatabaseByteCount  uint64
	TotalElapsed       time.Duration
	Trawlers           []IndexedTrawlerStatus
}
