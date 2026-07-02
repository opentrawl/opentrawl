package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCrawler struct {
	name         string
	metadata     string
	metadataExit int
	status       string
	statusExit   int
	doctor       string
	doctorExit   int
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Execute(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), ExitCode(err)
}

func writeFakeCrawlers(t *testing.T, crawlers ...fakeCrawler) string {
	t.Helper()
	dir := t.TempDir()
	for _, crawler := range crawlers {
		writeFakeCrawler(t, dir, crawler)
	}
	return dir
}

func writeFakeCrawler(t *testing.T, dir string, crawler fakeCrawler) {
	t.Helper()
	if crawler.metadata == "" && crawler.metadataExit == 0 {
		crawler.metadata = metadataJSON(crawler.name)
	}
	if crawler.status == "" && crawler.statusExit == 0 {
		crawler.status = statusJSON(crawler.name, "ok")
	}
	if crawler.doctor == "" && crawler.doctorExit == 0 {
		crawler.doctor = `{"checks":[{"id":"source_store","state":"ok"}]}`
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$#" -ne 2 ]; then
  exit 64
fi
case "$1 $2" in
  "metadata --json")
    printf '%%s\n' %s
    exit %d
    ;;
  "status --json")
    printf '%%s\n' %s
    exit %d
    ;;
  "doctor --json")
    printf '%%s\n' %s
    exit %d
    ;;
esac
exit 64
`, shellQuote(crawler.metadata), crawler.metadataExit, shellQuote(crawler.status), crawler.statusExit, shellQuote(crawler.doctor), crawler.doctorExit)
	path := filepath.Join(dir, crawler.name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func metadataJSON(id string) string {
	return fmt.Sprintf(`{"schema_version":1,"contract_version":1,"id":%q,"display_name":%q}`, id, id)
}

func statusJSON(id, state string) string {
	return fmt.Sprintf(`{"app_id":%q,"state":%q,"freshness":{"last_sync":"2026-07-02T14:03:00Z"},"counts":[{"id":"messages","label":"messages","value":12345}],"auth":{"authorized":true,"expires":null}}`, id, state)
}

func failingDoctorJSON() string {
	return `{"checks":[{"id":"tcc_full_disk_access","state":"fail","message":"cannot read the source database","remedy":"grant Full Disk Access to Trawl in System Settings > Privacy"}]}`
}
