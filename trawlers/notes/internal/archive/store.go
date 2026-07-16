package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// ErrSchemaOutdated means the archive on disk was written by an older build:
// its recorded schema_migrations version is behind SchemaVersion. This is a
// single-user, local-first archive rebuilt from the source Notes store at
// every sync, so there is no in-place migration path -- trawl sync notes
// parks the old file (see parkArchiveFile) and rebuilds a fresh one from
// scratch. Read verbs surface this error as-is; only sync parks and rebuilds.
var ErrSchemaOutdated = errors.New("archive is from an older build; trawl sync notes will park it and rebuild")

// ErrSchemaNewer means the archive on disk was written by a newer build than
// this one supports. An old binary must never demote a newer archive, so
// this is never parked and never rebuilt -- it is a hard stop.
var ErrSchemaNewer = errors.New("archive schema is newer than this build of trawl notes supports")

type Store struct {
	store *store.Store
	path  string
	owned bool
}

func DefaultPaths() config.Paths {
	paths, _ := config.App{Name: AppID, BaseDir: "~/.opentrawl/" + AppID}.DefaultPaths()
	return paths
}

func Exists(path string) bool {
	if path == "" {
		path = DefaultPaths().DBPath
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Open opens (creating if absent) the archive at path for standalone callers
// that own the connection outright (tests, one-off tools). It applies the
// current schema and, if the file already holds an older, previously synced
// archive, parks it aside and starts fresh -- see ensureCurrentSchema.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPaths().DBPath
	}
	st, err := store.Open(ctx, store.Options{Path: path, Schema: schema})
	if err != nil {
		return nil, err
	}
	st, _, err = ensureCurrentSchema(ctx, st, path)
	if err != nil {
		return nil, err
	}
	if err := st.EnsureSchemaVersion(ctx, SchemaVersion); err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Store{store: st, path: path, owned: true}, nil
}

// Use wraps an already-open store connection (owned by the caller, typically
// the trawlkit CLI harness, which opens it before the crawler's sync verb
// runs and closes it afterwards). Use only ever borrows that connection --
// Close is a no-op and the caller's own Close does the work.
//
// Use never parks. Parking an older archive aside happens earlier, before
// the harness ever opens this connection (see PrepareArchive, called by
// trawlkit's write-open path ahead of req.Store). If a genuinely older,
// versioned archive still reaches Use, that earlier step was skipped -- Use
// refuses with ErrSchemaOutdated, exactly like a read verb, as defense in
// depth. It checks the version before touching the file with schema DDL, so
// a mistaken call here never mutates a file it is about to reject.
func Use(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	if version > SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaNewer, version, SchemaVersion)
	}
	if version != 0 && version < SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaOutdated, version, SchemaVersion)
	}
	if _, err := st.DB().ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := st.EnsureSchemaVersion(ctx, SchemaVersion); err != nil {
		return nil, err
	}
	return &Store{store: st, path: path}, nil
}

// PrepareArchive peeks the archive file's recorded schema version with a
// disposable, read-only connection -- no schema DDL, no writes -- and parks
// it aside if it is a genuine older, versioned archive. It is meant to run
// before anything opens a long-lived write connection on path, so the file
// that gets parked is always byte-identical to what sync found: nothing
// (not even an idempotent "create table if not exists") has touched it yet.
//
//   - No file yet, or a file with no schema_migrations table: version 0,
//     nothing recorded to protect, no-op.
//   - Recorded version equals SchemaVersion: no-op.
//   - Recorded version is older: park it aside at path.v<version> forever;
//     whatever opens path next starts a fresh archive.
//   - Recorded version is newer: ErrSchemaNewer. This build must not touch
//     it, parked or otherwise.
//
// This implements trawlkit.ArchivePreparer; the CLI harness calls it ahead
// of opening req.Store for every mutating notes verb (sync, sync-store).
func PrepareArchive(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		path = DefaultPaths().DBPath
	}
	if !Exists(path) {
		return nil
	}
	version, err := peekSchemaVersion(ctx, path)
	if err != nil {
		return fmt.Errorf("peek archive schema: %w", err)
	}
	if version > SchemaVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrSchemaNewer, version, SchemaVersion)
	}
	if version == 0 || version == SchemaVersion {
		return nil
	}
	if err := parkArchiveFile(path, version); err != nil {
		return err
	}
	return nil
}

// peekSchemaVersion opens path with a disposable, immutable read-only
// connection that applies no schema DDL and leaves no -wal/-shm sidecars
// behind (plain mode=ro would create them; the file about to be parked must
// stay byte-identical, siblings included). It reads the recorded
// schema_migrations version and closes immediately. A missing
// schema_migrations table (a pre-versioned or brand new file) reads as
// version 0. Immutable is safe here: PrepareArchive runs under the mutate
// run-lock before any connection opens, so the file is quiescent.
func peekSchemaVersion(ctx context.Context, path string) (int, error) {
	st, err := store.OpenForeignReadOnly(ctx, path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close() }()
	return st.SchemaVersion(ctx)
}

// UseExisting wraps an already-open, read-only store connection for a read
// verb. It never parks or rewrites anything: an older archive gets
// ErrSchemaOutdated (sync will fix it), a newer archive gets ErrSchemaNewer
// (this build cannot touch it), and only an exact schema match succeeds.
func UseExisting(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	if version > SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaNewer, version, SchemaVersion)
	}
	if version < SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaOutdated, version, SchemaVersion)
	}
	return &Store{store: st, path: path}, nil
}

// ensureCurrentSchema is Open's decision point for what a standalone,
// caller-owned connection does about a mismatched schema version. st is
// already open and schema-applied at path. It is safe for Open to close and
// reopen st here because Open's caller never keeps its own handle to st past
// the call -- the returned *Store is the only reference. Use does not call
// this: req.Store is owned by the trawlkit harness, which keeps using it
// after the crawler's verb returns, so closing or swapping it here would
// leave the harness holding a dead connection (see Use's doc comment and
// PrepareArchive, which parks before req.Store is ever opened).
//
//   - Recorded version > SchemaVersion: this binary is older than the
//     archive. Refused outright, never parked, never touched.
//   - Recorded version == SchemaVersion: nothing to do.
//   - Recorded version == 0: schema_migrations has never been stamped. That
//     is either a brand new, never-synced archive (nothing to preserve) or a
//     sync that crashed before recording its version (the resumed sync
//     safely re-applies over the same rows via upserts). Either way there is
//     no prior *versioned* archive to protect, so this proceeds in place.
//   - Recorded version is otherwise older: a genuine archive from an older
//     build already completed at least one synced, versioned state. st is
//     closed, the underlying file (and its -wal/-shm siblings) are parked
//     aside forever, and a fresh store is opened at the same path.
//
// On success it returns the store to use going forward and whether that
// store is a freshly opened one the caller now owns (rebuilt=true) as
// opposed to the connection it was handed (rebuilt=false). On any error the
// original st has already been closed.
func ensureCurrentSchema(ctx context.Context, st *store.Store, path string) (out *store.Store, rebuilt bool, err error) {
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		_ = st.Close()
		return nil, false, err
	}
	if version > SchemaVersion {
		_ = st.Close()
		return nil, false, fmt.Errorf("%w: got %d, want %d", ErrSchemaNewer, version, SchemaVersion)
	}
	if version == SchemaVersion || version == 0 {
		return st, false, nil
	}
	if err := st.Close(); err != nil {
		return nil, false, err
	}
	if err := parkArchiveFile(path, version); err != nil {
		return nil, false, err
	}
	fresh, err := store.Open(ctx, store.Options{Path: path, Schema: schema})
	if err != nil {
		return nil, false, err
	}
	return fresh, true, nil
}

// parkArchiveFile moves the archive file at path aside to path.v<version>,
// forever -- it is never deleted, only ever added to. Its -wal and -shm WAL
// siblings move alongside it if present, and move first: if a rename fails
// partway through, the main file is left either fully parked with both
// siblings already at their parked names, or still at path with no sibling
// renamed out from under it -- never at path paired with a sibling that has
// already moved to its parked name. The main file must exist (the caller
// just closed a connection to it, or PrepareArchive already confirmed it
// exists); the WAL siblings are best-effort, since a clean Close ordinarily
// checkpoints and removes them.
func parkArchiveFile(path string, version int) error {
	suffix := fmt.Sprintf(".v%d", version)
	if err := parkFile(path+"-wal", path+suffix+"-wal", false); err != nil {
		return err
	}
	if err := parkFile(path+"-shm", path+suffix+"-shm", false); err != nil {
		return err
	}
	if err := parkFile(path, path+suffix, true); err != nil {
		return err
	}
	return nil
}

// parkFile renames from to to. A missing source is only an error when
// required (the main archive file); WAL/SHM siblings are optional. An
// existing destination is always an error -- parking never overwrites a
// previously parked file.
func parkFile(from, to string, required bool) error {
	if _, err := os.Stat(from); err != nil {
		if os.IsNotExist(err) && !required {
			return nil
		}
		return fmt.Errorf("park %s: %w", from, err)
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("park %s: destination %s already exists", from, to)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat park destination %s: %w", to, err)
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("park %s: %w", from, err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.store == nil || !s.owned {
		return nil
	}
	return s.store.Close()
}

func (s *Store) DB() *sql.DB {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.DB()
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
