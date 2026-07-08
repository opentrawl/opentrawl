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

func TestSQLiteIndexLookupReturnsCanonicalRef(t *testing.T) {
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
	if err := index.UpsertCanonical(ctx, "22222", "legacy:item/1", "canonical:item/1"); err != nil {
		t.Fatal(err)
	}

	got, err := index.Lookup(ctx, "22222")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"canonical:item/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lookup returned %#v, want %#v", got, want)
	}

	aliases, err := index.Aliases(ctx, []string{"canonical:item/1"})
	if err != nil {
		t.Fatal(err)
	}
	if aliases["canonical:item/1"] != "22222" {
		t.Fatalf("Aliases = %#v, want canonical:item/1 -> 22222", aliases)
	}
}

func TestEnsureSchemaMigratesTableWithoutCanonicalRef(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/shortrefs.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `
create table short_refs (
  alias text not null,
  full_ref text not null,
  primary key (alias, full_ref)
);
insert into short_refs(alias, full_ref) values ('22222', 'legacy:item/1');
`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	got, err := NewSQLiteIndex(db).Lookup(ctx, "22222")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"legacy:item/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lookup after migration = %#v, want %#v", got, want)
	}
}
