package photos

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/store"
)

func TestSQLiteSnapshotProviderReadsSyntheticLibrary(t *testing.T) {
	t.Parallel()
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := createSyntheticPhotosDB(db.DB()); err != nil {
		t.Fatal(err)
	}

	snapshot, err := SQLiteSnapshotProvider{}.Snapshot(context.Background(), libraryPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Provider != "photos_sqlite_snapshot" {
		t.Fatalf("provider = %q", snapshot.Provider)
	}
	if len(snapshot.Assets) != 1 {
		t.Fatalf("assets = %d, want 1", len(snapshot.Assets))
	}
	asset := snapshot.Assets[0]
	if asset.MediaType != "image" || asset.CreationDate != "2026-05-28T10:00:00Z" {
		t.Fatalf("asset = %#v", asset)
	}
	if asset.Location == nil || asset.Location.Latitude != 52.3676 || asset.Location.Longitude != 4.9041 {
		t.Fatalf("location = %#v", asset.Location)
	}
	if len(asset.Resources) != 1 || !asset.Resources[0].NeedsDownload || asset.Resources[0].Availability != "remote" {
		t.Fatalf("resources = %#v", asset.Resources)
	}
	if len(asset.Albums) != 1 || asset.Albums[0].AlbumTitle != "Synthetic Album" {
		t.Fatalf("albums = %#v", asset.Albums)
	}
	if snapshot.Metadata["snapshot"] != "crawlkit_sqlite_copy" || snapshot.Metadata["album_join_table"] != "Z_34ASSETS" {
		t.Fatalf("metadata = %#v", snapshot.Metadata)
	}
}

func TestFallbackProviderUsesSecondaryAfterPrimaryError(t *testing.T) {
	t.Parallel()
	provider := FallbackProvider{
		Primary:   failingProvider{},
		Secondary: staticProvider{snapshot: LibrarySnapshot{Provider: "secondary", Metadata: map[string]any{}}},
	}
	snapshot, err := provider.Snapshot(context.Background(), "library")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Provider != "secondary" || snapshot.Metadata["source_strategy"] != "fallback_after_primary_error" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestValidLocationRejectsPhotosSentinels(t *testing.T) {
	t.Parallel()
	if validLocation(-180, -180) {
		t.Fatal("-180,-180 should not be treated as a real location")
	}
	if validLocation(0, 0) {
		t.Fatal("0,0 should not be treated as a real location")
	}
	if !validLocation(12.34, 56.78) {
		t.Fatal("ordinary coordinates should be valid")
	}
}

func createSyntheticPhotosDB(db *sql.DB) error {
	statements := []string{
		`create table ZASSET (
			Z_PK integer primary key,
			ZUUID varchar,
			ZKIND integer,
			ZKINDSUBTYPE integer,
			ZDATECREATED timestamp,
			ZMODIFICATIONDATE timestamp,
			ZADDEDDATE timestamp,
			ZWIDTH integer,
			ZHEIGHT integer,
			ZDURATION float,
			ZFAVORITE integer,
			ZHIDDEN integer,
			ZAVALANCHEUUID varchar,
			ZLATITUDE float,
			ZLONGITUDE float,
			ZUNIFORMTYPEIDENTIFIER varchar,
			ZFILENAME varchar,
			ZTRASHEDSTATE integer
		)`,
		`create table ZADDITIONALASSETATTRIBUTES (
			ZASSET integer,
			ZTIMEZONENAME varchar,
			ZGPSHORIZONTALACCURACY float,
			ZORIGINALFILENAME varchar
		)`,
		`create table ZINTERNALRESOURCE (
			ZASSET integer,
			ZRESOURCETYPE integer,
			ZCOMPACTUTI varchar,
			ZDATALENGTH integer,
			ZSTABLEHASH varchar,
			ZFINGERPRINT varchar,
			ZLOCALAVAILABILITY integer,
			ZREMOTEAVAILABILITY integer,
			ZVERSION integer
		)`,
		`create table ZGENERICALBUM (
			Z_PK integer primary key,
			ZUUID varchar,
			ZTITLE varchar,
			ZKIND integer,
			ZCLOUDALBUMSUBTYPE integer,
			ZTRASHEDSTATE integer
		)`,
		`create table Z_34ASSETS (
			Z_34ALBUMS integer,
			Z_3ASSETS integer
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	created := coreDataSeconds("2026-05-28T10:00:00Z")
	if _, err := db.Exec(`
insert into ZASSET(Z_PK, ZUUID, ZKIND, ZKINDSUBTYPE, ZDATECREATED, ZMODIFICATIONDATE, ZADDEDDATE, ZWIDTH, ZHEIGHT, ZDURATION, ZFAVORITE, ZHIDDEN, ZAVALANCHEUUID, ZLATITUDE, ZLONGITUDE, ZUNIFORMTYPEIDENTIFIER, ZFILENAME, ZTRASHEDSTATE)
values (1, 'fixture-uuid-1', 0, 0, ?, ?, ?, 4032, 3024, 0, 1, 0, '', 52.3676, 4.9041, 'public.heic', 'synthetic.heic', 0)
`, created, created, created); err != nil {
		return err
	}
	if _, err := db.Exec(`insert into ZADDITIONALASSETATTRIBUTES(ZASSET, ZTIMEZONENAME, ZGPSHORIZONTALACCURACY, ZORIGINALFILENAME) values (1, 'Europe/Amsterdam', 8.25, 'synthetic.heic')`); err != nil {
		return err
	}
	if _, err := db.Exec(`insert into ZINTERNALRESOURCE(ZASSET, ZRESOURCETYPE, ZCOMPACTUTI, ZDATALENGTH, ZSTABLEHASH, ZFINGERPRINT, ZLOCALAVAILABILITY, ZREMOTEAVAILABILITY, ZVERSION) values (1, 0, 'public.heic', 12345, 'stable-hash', '', 0, 1, 1)`); err != nil {
		return err
	}
	if _, err := db.Exec(`insert into ZGENERICALBUM(Z_PK, ZUUID, ZTITLE, ZKIND, ZCLOUDALBUMSUBTYPE, ZTRASHEDSTATE) values (10, 'album-uuid-1', 'Synthetic Album', 2, 0, 0)`); err != nil {
		return err
	}
	_, err := db.Exec(`insert into Z_34ASSETS(Z_34ALBUMS, Z_3ASSETS) values (10, 1)`)
	return err
}

func coreDataSeconds(value string) int64 {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed.Unix() - 978307200
}

type failingProvider struct{}

func (failingProvider) Snapshot(context.Context, string) (LibrarySnapshot, error) {
	return LibrarySnapshot{}, errors.New("primary failed")
}

type staticProvider struct {
	snapshot LibrarySnapshot
}

func (p staticProvider) Snapshot(context.Context, string) (LibrarySnapshot, error) {
	return p.snapshot, nil
}
