package qa

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Fixtures struct {
	Home           string
	BinDir         string
	TelegramSource string
	BirdDump       string
}

func CreateOutputFixtures(home, repoRoot string) (Fixtures, error) {
	if home == "" {
		return Fixtures{}, fmt.Errorf("home is required")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return Fixtures{}, err
	}
	fixtures := Fixtures{
		Home:           home,
		BinDir:         filepath.Join(home, "bin"),
		TelegramSource: filepath.Join(home, "fixtures", "telegram"),
		BirdDump:       filepath.Join(home, "fixtures", "x-archive"),
	}
	if err := os.MkdirAll(fixtures.BinDir, 0o755); err != nil {
		return Fixtures{}, err
	}
	if err := createMessagesFixture(filepath.Join(home, "Library", "Messages", "chat.db")); err != nil {
		return Fixtures{}, err
	}
	if err := createCalendarFixture(filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar", "Calendar.sqlitedb")); err != nil {
		return Fixtures{}, err
	}
	if err := createPhotosFixture(filepath.Join(home, "Pictures", "Photos Library.photoslibrary")); err != nil {
		return Fixtures{}, err
	}
	if err := createWhatsAppFixture(filepath.Join(home, "fixtures", "whatsapp")); err != nil {
		return Fixtures{}, err
	}
	if err := writeCrawlerConfig(filepath.Join(home, ".opentrawl", "whatsapp", "config.toml"), filepath.Join(home, "fixtures", "whatsapp")); err != nil {
		return Fixtures{}, err
	}
	if err := createTelegramFixture(fixtures.TelegramSource, repoRoot); err != nil {
		return Fixtures{}, err
	}
	if err := createBirdDump(fixtures.BirdDump); err != nil {
		return Fixtures{}, err
	}
	if err := writeFakeGog(filepath.Join(fixtures.BinDir, "gog")); err != nil {
		return Fixtures{}, err
	}
	return fixtures, nil
}

func writeCrawlerConfig(path, source string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("source = \""+source+"\"\ncopy_media = true\n"), 0o600)
}

func openSQLite(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return sql.Open("sqlite3", path)
}

func execAll(db *sql.DB, statements ...string) error {
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func coreDate(t time.Time) float64 {
	return float64(t.Unix() - 978307200)
}

func touch(path string, at time.Time) error {
	return os.Chtimes(path, at, at)
}

func canaryRead(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var tables int
	return db.QueryRowContext(context.Background(), "select count(*) from sqlite_master").Scan(&tables)
}
