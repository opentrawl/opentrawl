package developmentcache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCheckpointRejectsArchiveIdentityWithoutMutation(t *testing.T) {
	paths := syntheticStoragePaths(t)
	paths.StatePath = paths.ArchivePath
	before, err := os.ReadFile(paths.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := OpenCheckpoint(context.Background(), paths)
	after, readErr := os.ReadFile(paths.ArchivePath)
	t.Logf("boundary=checkpoint_open input=%#v output_checkpoint_nil=%t output_error=%q archive_before=%q archive_after=%q", paths, checkpoint == nil, err, before, after)
	if err == nil || checkpoint != nil || readErr != nil || string(after) != string(before) {
		t.Fatalf("OpenCheckpoint = %#v, %v; archive before=%q after=%q read_error=%v", checkpoint, err, before, after, readErr)
	}
}

func TestOpenCheckpointRejectsArchiveHardLinkWithoutMutation(t *testing.T) {
	paths := syntheticStoragePaths(t)
	if err := os.Link(paths.ArchivePath, paths.StatePath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := OpenCheckpoint(context.Background(), paths)
	after, readErr := os.ReadFile(paths.ArchivePath)
	walExists := pathExists(paths.StatePath + "-wal")
	shmExists := pathExists(paths.StatePath + "-shm")
	t.Logf("boundary=checkpoint_hard_link input_archive=%q input_state=%q output_checkpoint_nil=%t output_error=%q archive_before=%q archive_after=%q wal_exists=%t shm_exists=%t", paths.ArchivePath, paths.StatePath, checkpoint == nil, err, before, after, walExists, shmExists)
	if err == nil || checkpoint != nil || readErr != nil || string(after) != string(before) || walExists || shmExists {
		t.Fatalf("OpenCheckpoint = %#v, %v; archive before=%q after=%q read_error=%v wal=%t shm=%t", checkpoint, err, before, after, readErr, walExists, shmExists)
	}
}

func TestStoragePathsRejectUnsafeCheckpointRelationshipsBeforeCreation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(StoragePaths) StoragePaths
		want   string
	}{
		{name: "archive identity", change: func(paths StoragePaths) StoragePaths { paths.StatePath = paths.ArchivePath; return paths }, want: "state aliases the read-only Photos archive"},
		{name: "state inside source", change: func(paths StoragePaths) StoragePaths {
			paths.StatePath = filepath.Join(paths.SourceRoot, "checkpoint.sqlite")
			return paths
		}, want: "source path overlaps the state path"},
		{name: "state inside cache", change: func(paths StoragePaths) StoragePaths {
			paths.StatePath = filepath.Join(paths.CacheRoot, "checkpoint.sqlite")
			return paths
		}, want: "cache path overlaps the state path"},
		{name: "archive inside cache", change: func(paths StoragePaths) StoragePaths {
			inside := filepath.Join(paths.CacheRoot, "photos.db")
			if err := os.WriteFile(inside, []byte("synthetic overlapping archive"), 0o600); err != nil {
				t.Fatal(err)
			}
			paths.ArchivePath = inside
			return paths
		}, want: "archive path overlaps the cache path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := test.change(syntheticStoragePaths(t))
			before, err := os.ReadFile(paths.ArchivePath)
			if err != nil {
				t.Fatal(err)
			}
			output, err := ValidateStoragePaths(paths)
			t.Logf("boundary=path_relationship input=%#v output=%#v error=%q", paths, output, err)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateStoragePaths error = %v, want %q", err, test.want)
			}
			after, readErr := os.ReadFile(paths.ArchivePath)
			if readErr != nil || string(after) != string(before) {
				t.Fatalf("archive changed: before=%q after=%q error=%v", before, after, readErr)
			}
			if _, statErr := os.Lstat(filepath.Join(paths.CacheRoot, ".locks")); !os.IsNotExist(statErr) {
				t.Fatalf("path validation created cache state: %v", statErr)
			}
		})
	}
}

func TestStoragePathsRejectSymlinkModeAndOwnerBeforeCreation(t *testing.T) {
	t.Run("state symlink", func(t *testing.T) {
		paths := syntheticStoragePaths(t)
		target := filepath.Join(filepath.Dir(paths.StatePath), "target.sqlite")
		before := []byte("synthetic state symlink target")
		if err := os.WriteFile(target, before, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, paths.StatePath); err != nil {
			t.Fatal(err)
		}
		_, err := ValidateStoragePaths(paths)
		after, readErr := os.ReadFile(target)
		t.Logf("boundary=state_symlink input_state=%q input_target=%q output_error=%q target_before=%q target_after=%q", paths.StatePath, target, err, before, after)
		if err == nil || !strings.Contains(err.Error(), "symlink component") || readErr != nil || string(after) != string(before) {
			t.Fatalf("ValidateStoragePaths error = %v target before=%q after=%q read_error=%v", err, before, after, readErr)
		}
	})

	t.Run("symlink component", func(t *testing.T) {
		paths := syntheticStoragePaths(t)
		realParent := filepath.Join(filepath.Dir(paths.SourceRoot), "real-parent")
		if err := os.Mkdir(realParent, 0o700); err != nil {
			t.Fatal(err)
		}
		realSource := filepath.Join(realParent, "Source.photoslibrary")
		if err := os.Mkdir(realSource, 0o700); err != nil {
			t.Fatal(err)
		}
		linkParent := filepath.Join(filepath.Dir(paths.SourceRoot), "linked-parent")
		if err := os.Symlink(realParent, linkParent); err != nil {
			t.Fatal(err)
		}
		paths.SourceRoot = filepath.Join(linkParent, filepath.Base(realSource))
		_, err := ValidateStoragePaths(paths)
		t.Logf("boundary=path_symlink input=%#v output_error=%q", paths, err)
		if err == nil || !strings.Contains(err.Error(), "symlink component") {
			t.Fatalf("ValidateStoragePaths error = %v, want symlink component rejection", err)
		}
		assertStateNotCreated(t, paths.StatePath)
	})

	t.Run("unsafe mode", func(t *testing.T) {
		paths := syntheticStoragePaths(t)
		if err := os.Chmod(paths.CacheRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := ValidateStoragePaths(paths)
		t.Logf("boundary=path_mode input_cache=%q input_mode=0755 output_error=%q", paths.CacheRoot, err)
		if err == nil || !strings.Contains(err.Error(), "owner-only 0700") {
			t.Fatalf("ValidateStoragePaths error = %v, want mode rejection", err)
		}
		assertStateNotCreated(t, paths.StatePath)
	})

	t.Run("wrong owner", func(t *testing.T) {
		paths := syntheticStoragePaths(t)
		original := currentEffectiveUID
		currentEffectiveUID = func() int { return original() + 1 }
		defer func() { currentEffectiveUID = original }()
		_, err := ValidateStoragePaths(paths)
		t.Logf("boundary=path_owner input_archive=%q input_effective_uid=%d output_error=%q", paths.ArchivePath, currentEffectiveUID(), err)
		if err == nil || !strings.Contains(err.Error(), "owner-controlled") {
			t.Fatalf("ValidateStoragePaths error = %v, want owner rejection", err)
		}
		assertStateNotCreated(t, paths.StatePath)
	})
}

func syntheticStoragePaths(t *testing.T) StoragePaths {
	t.Helper()
	root := canonicalStorageTempDir(t)
	paths := StoragePaths{
		ArchivePath: filepath.Join(root, "photos.db"),
		SourceRoot:  filepath.Join(root, "Source.photoslibrary"),
		CacheRoot:   filepath.Join(root, "cache"),
		StatePath:   filepath.Join(root, "state", "checkpoint.sqlite"),
	}
	for _, path := range []string{paths.SourceRoot, paths.CacheRoot, filepath.Dir(paths.StatePath)} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.ArchivePath, []byte("synthetic archive remains unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	return paths
}

func canonicalStorageTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertStateNotCreated(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("unsafe validation created state at %q: %v", path, err)
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
