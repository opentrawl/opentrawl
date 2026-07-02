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
	search       string
	searchExit   int
	searchLimit  string
	open         string
	openExit     int
	openRef      string
	sync         string
	syncExit     int
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
	if crawler.search == "" && crawler.searchExit == 0 {
		crawler.search = searchJSON("query")
	}
	if crawler.open == "" && crawler.openExit == 0 {
		crawler.open = `{"body":"Example body","ref":"msg/1"}`
	}
	if crawler.sync == "" && crawler.syncExit == 0 {
		crawler.sync = `{"state":"ok","message":"0 new items"}`
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$#" -lt 2 ]; then
  exit 64
fi
expected_open_ref=%s
expected_search_limit=%s
case "$1" in
  "metadata")
    if [ "$#" -ne 2 ] || [ "$2" != "--json" ]; then
      exit 64
    fi
    printf '%%s\n' %s
    exit %d
    ;;
  "status")
    if [ "$#" -ne 2 ] || [ "$2" != "--json" ]; then
      exit 64
    fi
    printf '%%s\n' %s
    exit %d
    ;;
  "doctor")
    if [ "$#" -ne 2 ] || [ "$2" != "--json" ]; then
      exit 64
    fi
    printf '%%s\n' %s
    exit %d
    ;;
  "search")
    if [ "$#" -lt 5 ] || [ "$3" != "--json" ] || [ "$4" != "--limit" ]; then
      exit 64
    fi
    if [ -n "$expected_search_limit" ] && [ "$5" != "$expected_search_limit" ]; then
      exit 64
    fi
    printf '%%s\n' %s
    exit %d
    ;;
  "open")
    if [ "$#" -ne 3 ] || [ "$3" != "--json" ]; then
      exit 64
    fi
    if [ -n "$expected_open_ref" ] && [ "$2" != "$expected_open_ref" ]; then
      exit 64
    fi
    printf '%%s\n' %s
    exit %d
    ;;
  "sync")
    if [ "$#" -ne 2 ] || [ "$2" != "--json" ]; then
      exit 64
    fi
    printf '%%s\n' %s
    exit %d
    ;;
esac
exit 64
`, shellQuote(crawler.openRef), shellQuote(crawler.searchLimit), shellQuote(crawler.metadata), crawler.metadataExit, shellQuote(crawler.status), crawler.statusExit, shellQuote(crawler.doctor), crawler.doctorExit, shellQuote(crawler.search), crawler.searchExit, shellQuote(crawler.open), crawler.openExit, shellQuote(crawler.sync), crawler.syncExit)
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

func searchJSON(query string) string {
	return fmt.Sprintf(`{"query":%q,"results":[],"total_matches":0,"truncated":false}`, query)
}

func failingDoctorJSON() string {
	return `{"checks":[{"id":"tcc_full_disk_access","state":"fail","message":"cannot read the source database","remedy":"grant Full Disk Access to Trawl in System Settings > Privacy"}]}`
}
