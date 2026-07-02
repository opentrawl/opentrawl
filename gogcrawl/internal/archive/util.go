package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	ckstore "github.com/openclaw/crawlkit/store"
)

const RefPrefix = "gogcrawl:msg/"

func countTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, `select count(*) from `+table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func ftsQuery(query string) string {
	value := ckstore.FTS5TokenQuery(query)
	if value != "" {
		return value
	}
	return ckstore.FTS5Phrase(query)
}

func labelsJSON(labels []string) string {
	data, err := json.Marshal(labels)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func parseLabels(value string) []string {
	var labels []string
	if err := json.Unmarshal([]byte(value), &labels); err != nil {
		return nil
	}
	return labels
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func formatArchiveTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format(time.RFC3339)
}

func parseRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	id, ok := strings.CutPrefix(ref, RefPrefix)
	if !ok || strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("ref must look like %s<gmail-message-id>", RefPrefix)
	}
	return strings.TrimSpace(id), nil
}

func displaySender(name, address string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if strings.TrimSpace(address) != "" {
		return strings.TrimSpace(address)
	}
	return "unknown sender"
}
