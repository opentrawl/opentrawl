package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAppliesSchemaPragmasAndPermissions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "archive.db")
	st, err := Open(ctx, Options{
		Path:          path,
		Schema:        `create table things(id text primary key, value text not null);`,
		SchemaVersion: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if st.Path() != path {
		t.Fatalf("path = %q, want %q", st.Path(), path)
	}

	var journalMode string
	if err := st.DB().QueryRowContext(ctx, `pragma journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode = %q", journalMode)
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("schema version = %d", version)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		got := info.Mode().Perm()
		t.Fatalf("mode = %o", got)
	}
}

func TestWithTxAndQuery(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Options{
		Path:   filepath.Join(t.TempDir(), "archive.db"),
		Schema: `create table things(id text primary key, value text not null);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `insert into things(id, value) values('a', 'one')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	result, err := st.Query(ctx, `select id, value from things`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Values[0]["value"] != "one" {
		t.Fatalf("unexpected query result: %+v", result)
	}
}

func TestReadOnlyRejectsWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "archive.db")
	st, err := Open(ctx, Options{
		Path:   path,
		Schema: `create table things(id text primary key);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if _, err := ro.DB().ExecContext(ctx, `insert into things(id) values('x')`); err == nil {
		t.Fatal("expected readonly write to fail")
	}
}

func TestOpenEscapesURIReservedPathCharacters(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	name := "archive?tenant=a#frag.db"
	if runtime.GOOS == "windows" {
		name = "archive#frag.db"
	}
	path := filepath.Join(dir, name)
	st, err := Open(ctx, Options{
		Path:   path,
		Schema: `create table things(id text primary key, value text not null);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `insert into things(id, value) values('a', 'one')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("literal database path missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive")); !os.IsNotExist(err) {
		t.Fatalf("unexpected truncated database stat err = %v", err)
	}

	ro, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	var value string
	if err := ro.DB().QueryRowContext(ctx, `select value from things where id = 'a'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "one" {
		t.Fatalf("value = %q", value)
	}
}

func TestDSNUsesAbsoluteWindowsFileURI(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific file URI")
	}
	got := dsn(`C:\Users\runner\archive.db`, "_pragma=foreign_keys(1)")
	if !strings.HasPrefix(got, "file:///C:/Users/runner/archive.db?") {
		t.Fatalf("dsn = %q", got)
	}
}

func TestFTS5Helpers(t *testing.T) {
	query, err := FTS5Terms(`hello "quoted" world`, "AND")
	if err != nil {
		t.Fatal(err)
	}
	if query != `"hello" AND """quoted""" AND "world"` {
		t.Fatalf("query = %q", query)
	}
	if _, err := FTS5Terms("hello", "NEAR"); err == nil {
		t.Fatal("unsupported operator should fail")
	}
	if query := FTS5TokenQuery(`scope-upgrade OR café_2`); query != `"scope" "upgrade" "OR" "café_2"` {
		t.Fatalf("token query = %q", query)
	}
	if query := FTS5TokenQuery("e\u0301clair"); query != "\"e\u0301clair\"" {
		t.Fatalf("decomposed Unicode token query = %q", query)
	}
	if snippet := FTS5Snippet("the sooner the better!", "the"); snippet != "the sooner the better!" {
		t.Fatalf("snippet = %q", snippet)
	}
	if snippet := FTS5Snippet("alpha\n\t beta   gamma", "beta"); snippet != "alpha beta gamma" {
		t.Fatalf("collapsed snippet = %q", snippet)
	}
	longText := strings.Join([]string{
		"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india",
		"juliet", "kilo", "lima", "the", "sooner", "the", "better", "november", "oscar",
		"papa", "quebec", "romeo", "sierra", "tango", "uniform", "victor", "whiskey",
		"xray", "yankee", "zulu",
	}, " ")
	snippet := FTS5Snippet(longText, "better")
	if len([]rune(snippet)) > 120 {
		t.Fatalf("snippet length = %d, want <= 120: %q", len([]rune(snippet)), snippet)
	}
	for _, marker := range []string{"[", "]", "...", "…", "\n", "\t"} {
		if strings.Contains(snippet, marker) {
			t.Fatalf("snippet contains marker/whitespace %q: %q", marker, snippet)
		}
	}
	if !strings.Contains(snippet, "the sooner the better") {
		t.Fatalf("snippet missed match context: %q", snippet)
	}
	if strings.HasPrefix(snippet, "lie") || strings.HasSuffix(snippet, "cto") {
		t.Fatalf("snippet cut a word: %q", snippet)
	}
	for _, input := range []string{"", `-- ""`, `*:^()`} {
		if query := FTS5TokenQuery(input); query != "" {
			t.Fatalf("empty/punctuation-only token query = %q", query)
		}
	}

	ctx := context.Background()
	st, err := Open(ctx, Options{
		Path:   filepath.Join(t.TempDir(), "fts.db"),
		Schema: `create virtual table docs using fts5(id unindexed, body);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	docs := []struct {
		id   string
		body string
	}{
		{id: "plain", body: "scope upgrade"},
		{id: "spaced", body: "scope filler upgrade"},
		{id: "operator", body: "foo OR bar"},
		{id: "unicode", body: "café_2 東京 мир"},
		{id: "decomposed", body: "e\u0301clair"},
	}
	for _, doc := range docs {
		if _, err := st.DB().ExecContext(ctx, `insert into docs(id, body) values (?, ?)`, doc.id, doc.body); err != nil {
			t.Fatal(err)
		}
	}
	queries := []struct {
		input string
		want  string
	}{
		{input: `scope-upgrade`, want: "plain,spaced"},
		{input: `foo" OR bar*`, want: "operator"},
		{input: `café_2 東京 мир`, want: "unicode"},
		{input: "e\u0301clair", want: "decomposed"},
	}
	for _, tt := range queries {
		var got string
		query := FTS5TokenQuery(tt.input)
		err := st.DB().QueryRowContext(ctx, `
			select coalesce(group_concat(id, ','), '')
			from (select id from docs where docs match ? order by id)
		`, query).Scan(&got)
		if err != nil {
			t.Fatalf("query %q: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("query %q matches %q, want %q", tt.input, got, tt.want)
		}
	}
	if err := OptimizeFTS5(ctx, st.DB(), "docs"); err != nil {
		t.Fatal(err)
	}
	if err := OptimizeFTS5(ctx, st.DB(), `bad"table`); err == nil {
		t.Fatal("unsafe FTS table should fail")
	}
}

func TestRouteFTS5Query(t *testing.T) {
	tests := []struct {
		input        string
		mode         FTS5QueryMode
		match        string
		likePatterns []string
	}{
		{input: "launch", mode: FTS5QueryMatch, match: `"launch"`},
		{input: "鹤鸣茶社", mode: FTS5QueryMatch, match: `"鹤鸣茶社"`},
		{input: "鸣茶社", mode: FTS5QueryMatch, match: `"鸣茶社"`},
		{input: "茶社", mode: FTS5QueryLike, likePatterns: []string{"%茶社%"}},
		{input: "北京", mode: FTS5QueryLike, likePatterns: []string{"%北京%"}},
		{input: "tea 茶社", mode: FTS5QueryLike, likePatterns: []string{"%tea%", "%茶社%"}},
		{input: "tea 鹤鸣茶社", mode: FTS5QueryMatch, match: `"tea" "鹤鸣茶社"`},
		{input: "北京 上海", mode: FTS5QueryLike, likePatterns: []string{"%北京%", "%上海%"}},
		{input: "100% 茶社", mode: FTS5QueryLike, likePatterns: []string{"%100%", "%茶社%"}},
	}
	for _, tt := range tests {
		got := RouteFTS5Query(tt.input)
		if got.Mode != tt.mode {
			t.Fatalf("%q mode = %q, want %q", tt.input, got.Mode, tt.mode)
		}
		if got.Match != tt.match {
			t.Fatalf("%q match = %q, want %q", tt.input, got.Match, tt.match)
		}
		if strings.Join(got.LikePatterns, "\x00") != strings.Join(tt.likePatterns, "\x00") {
			t.Fatalf("%q like patterns = %#v, want %#v", tt.input, got.LikePatterns, tt.likePatterns)
		}
	}
}

func TestRouteFTS5QueryAgainstTrigramTable(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Options{
		Path:   filepath.Join(t.TempDir(), "trigram.db"),
		Schema: `create virtual table docs using fts5(body, ` + FTS5TrigramTokenizer + `);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, body := range []string{
		"meet at 鹤鸣茶社 for launch planning",
		"tea notes from 北京",
	} {
		if _, err := st.DB().ExecContext(ctx, `insert into docs(body) values (?)`, body); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		query string
		want  int
	}{
		{query: "茶社", want: 1},
		{query: "鸣茶社", want: 1},
		{query: "鹤鸣茶社", want: 1},
		{query: "launch", want: 1},
		{query: "tea 北京", want: 1},
	}
	for _, tt := range tests {
		plan := RouteFTS5Query(tt.query)
		where, args := testFTS5Where("docs", []string{"body"}, plan)
		var got int
		if err := st.DB().QueryRowContext(ctx, `select count(*) from docs where `+where, args...).Scan(&got); err != nil {
			t.Fatalf("%q query failed: %v", tt.query, err)
		}
		if got != tt.want {
			t.Fatalf("%q matches %d rows, want %d", tt.query, got, tt.want)
		}
	}
}

func testFTS5Where(table string, columns []string, plan FTS5QueryPlan) (string, []any) {
	if plan.Mode == FTS5QueryMatch {
		return table + " match ?", []any{plan.Match}
	}
	var parts []string
	var args []any
	for _, pattern := range plan.LikePatterns {
		var columnParts []string
		for _, column := range columns {
			columnParts = append(columnParts, QuoteIdent(column)+" like ? escape '\\'")
			args = append(args, pattern)
		}
		parts = append(parts, "("+strings.Join(columnParts, " or ")+")")
	}
	return strings.Join(parts, " and "), args
}
