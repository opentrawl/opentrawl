package telegramdesktop

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxProbeBytes = 16

type Options struct {
	Path string
}

type Report struct {
	Path         string   `json:"path"`
	Product      string   `json:"product"`
	Explicit     bool     `json:"explicit"`
	Exists       bool     `json:"exists"`
	Accessible   bool     `json:"accessible"`
	Store        string   `json:"store"`
	SQLiteFiles  int      `json:"sqlite_files"`
	KeyFiles     int      `json:"key_files,omitempty"`
	PostboxDBs   int      `json:"postbox_dbs,omitempty"`
	AccountDirs  int      `json:"account_dirs,omitempty"`
	FilesScanned int      `json:"files_scanned"`
	BytesScanned int64    `json:"bytes_scanned"`
	DryRun       bool     `json:"dry_run,omitempty"`
	Samples      []Sample `json:"samples,omitempty"`
	Note         string   `json:"note,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type Sample struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}

func DefaultPostboxPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Group Containers", "6N38VWS5BX.ru.keepcoder.Telegram")
}

func Probe(ctx context.Context, opts Options) Report {
	source := resolveImportSource(ctx, strings.TrimSpace(opts.Path))
	report := probePath(ctx, source.path)
	report.Product = string(source.product)
	report.Explicit = source.explicit
	return report
}

func probePath(ctx context.Context, path string) Report {
	report := Report{Path: path, Store: "missing"}
	info, err := os.Stat(path)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Exists = true
	if !info.IsDir() {
		report.Store = "unsupported-file"
		report.Error = "path is not a directory"
		return report
	}
	err = filepath.WalkDir(path, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if report.Error == "" {
				report.Error = walkErr.Error()
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if isLikelyAccountDir(entry.Name()) && p != path {
				report.AccountDirs++
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if report.Error == "" {
				report.Error = err.Error()
			}
			return nil
		}
		kind, ok := sniffFile(p)
		if !ok {
			return nil
		}
		report.FilesScanned++
		report.BytesScanned += minInt64(info.Size(), maxProbeBytes)
		switch kind {
		case "sqlite":
			report.SQLiteFiles++
		case "postbox-key":
			report.KeyFiles++
		case "postbox-db":
			report.PostboxDBs++
		}
		if len(report.Samples) < 8 {
			report.Samples = append(report.Samples, Sample{Path: p, Kind: kind, Size: info.Size()})
		}
		return nil
	})
	if err != nil {
		report.Error = err.Error()
	}
	report.Accessible = report.FilesScanned > 0 && report.Error == ""
	switch {
	case report.KeyFiles > 0 && report.PostboxDBs > 0:
		report.Store = "telegram-macos-postbox"
		report.Note = "Native Telegram for macOS Postbox data is readable locally; import archives cached media, and --fetch-media can fetch missing cloud media from the existing native session"
	case report.SQLiteFiles > 0:
		report.Store = "sqlite"
	case report.FilesScanned > 0:
		report.Store = "unknown"
	default:
		report.Store = "empty"
	}
	return report
}

func LooksLikePostbox(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultPostboxPath()
	}
	root := filepath.Clean(path)
	if hasPostboxAccount(root) && fileExists(filepath.Join(filepath.Dir(root), ".tempkeyEncrypted")) {
		return true
	}
	if hasPostboxLane(root) {
		return true
	}
	for _, name := range []string{"stable", "appstore"} {
		if hasPostboxLane(filepath.Join(root, name)) {
			return true
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && hasPostboxLane(filepath.Join(root, entry.Name())) {
			return true
		}
	}
	return false
}

func hasPostboxLane(path string) bool {
	if !fileExists(filepath.Join(path, ".tempkeyEncrypted")) {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(path, "account-*", "postbox", "db", "db_sqlite"))
	return err == nil && len(matches) > 0
}

func hasPostboxAccount(path string) bool {
	return fileExists(filepath.Join(path, "postbox", "db", "db_sqlite"))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func sniffFile(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	var header [maxProbeBytes]byte
	n, err := io.ReadFull(f, header[:])
	if err != nil && !errorsIsEOF(err) {
		return "", false
	}
	buf := header[:n]
	switch {
	case filepath.Base(path) == ".tempkeyEncrypted":
		return "postbox-key", true
	case filepath.Base(path) == "db_sqlite" && filepath.Base(filepath.Dir(path)) == "db" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "postbox":
		return "postbox-db", true
	case bytes.HasPrefix(buf, []byte("SQLite format 3")):
		return "sqlite", true
	default:
		return "other", true
	}
}

func errorsIsEOF(err error) bool {
	return err == io.EOF || err == io.ErrUnexpectedEOF
}

func isLikelyAccountDir(name string) bool {
	if strings.HasPrefix(name, "account-") && len(name) > len("account-") {
		for _, r := range strings.TrimPrefix(name, "account-") {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
