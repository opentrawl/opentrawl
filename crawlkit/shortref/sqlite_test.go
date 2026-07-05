package shortref

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteIndexRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/shortrefs.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	index := NewSQLiteIndex(db)

	displayEntries, err := buildWithAlias([]string{"source:a", "source:b", "source:c"}, MinLength, craftedAlias)
	if err != nil {
		t.Fatal(err)
	}
	lookupEntries := LookupEntries(displayEntries)
	if err := index.UpsertEntries(ctx, lookupEntries); err != nil {
		t.Fatal(err)
	}
	if err := index.UpsertEntries(ctx, lookupEntries); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		alias string
		want  []string
	}{
		{alias: "22222", want: []string{"source:a", "source:b"}},
		{alias: "22222a", want: []string{"source:a"}},
		{alias: "22222b", want: []string{"source:b"}},
		{alias: "33333", want: []string{"source:c"}},
		{alias: "zzzzz", want: []string{}},
	}
	for _, test := range tests {
		got, err := index.Lookup(ctx, test.alias)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("Lookup(%q) = %#v, want %#v", test.alias, got, test.want)
		}
	}
}
