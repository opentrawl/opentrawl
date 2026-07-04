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
	name          string
	metadata      string
	metadataExit  int
	status        string
	statusExit    int
	doctor        string
	doctorExit    int
	search        string
	searchExit    int
	searchLimit   string
	searchQuery   string
	searchNoQuery bool
	searchWho     string
	who           string
	whoExit       int
	whoQuery      string
	shortRefAlias string
	open          string
	openExit      int
	openRef       string
	openHuman     string
	openHumanExit int
	openStderr    string
	sync          string
	syncExit      int
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
	if crawler.openHuman == "" && crawler.openHumanExit == 0 {
		crawler.openHuman = "Example body"
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
expected_search_query=%s
expected_search_no_query=%s
expected_search_who=%s
expected_who_query=%s
expected_short_ref_alias=%s
open_human=%s
open_stderr=%s
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
    shift
    query=""
    found_json=""
    found_limit=""
    found_who=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        "--json")
          found_json="1"
          shift
          ;;
        "--limit")
          if [ "$#" -lt 2 ]; then
            exit 64
          fi
          found_limit="$2"
          shift 2
          ;;
        "--who")
          if [ "$#" -lt 2 ]; then
            exit 64
          fi
          found_who="$2"
          shift 2
          ;;
        "--after"|"--before")
          if [ "$#" -lt 2 ]; then
            exit 64
          fi
          shift 2
          ;;
        --*)
          exit 64
          ;;
        *)
          if [ -n "$query" ]; then
            exit 64
          fi
          query="$1"
          shift
          ;;
      esac
    done
    if [ "$found_json" != "1" ] || [ -z "$found_limit" ]; then
      exit 64
    fi
    if [ -n "$expected_search_query" ] && [ "$query" != "$expected_search_query" ]; then
      exit 64
    fi
    if [ "$expected_search_no_query" = "1" ] && [ -n "$query" ]; then
      exit 64
    fi
    if [ -n "$expected_search_limit" ] && [ "$found_limit" != "$expected_search_limit" ]; then
      exit 64
    fi
    if [ -n "$expected_search_who" ]; then
      if [ "$found_who" != "$expected_search_who" ]; then
        exit 64
      fi
    fi
    printf '%%s\n' %s
    exit %d
    ;;
  "who")
    if [ "$#" -ne 3 ] || [ "$3" != "--json" ]; then
      exit 64
    fi
    if [ -n "$expected_who_query" ] && [ "$2" != "$expected_who_query" ]; then
      exit 64
    fi
    printf '%%s\n' %s
    exit %d
    ;;
  "open")
    if [ "$#" -eq 3 ] && [ "$3" = "--json" ]; then
      expected_ref="$expected_open_ref"
      if [ -n "$expected_short_ref_alias" ]; then
        expected_ref="$expected_short_ref_alias"
      fi
      if [ -n "$expected_ref" ] && [ "$2" != "$expected_ref" ]; then
        exit 64
      fi
      printf '%%s\n' %s
      exit %d
    fi
    if [ "$#" -ne 2 ]; then
      exit 64
    fi
    if [ -n "$expected_open_ref" ] && [ "$2" != "$expected_open_ref" ]; then
      exit 64
    fi
    if [ -n "$open_stderr" ]; then
      printf '%%s\n' "$open_stderr" >&2
    fi
    if [ -n "$open_human" ]; then
      printf '%%s\n' "$open_human"
    fi
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
`, shellQuote(crawler.openRef), shellQuote(crawler.searchLimit), shellQuote(crawler.searchQuery), shellBool(crawler.searchNoQuery), shellQuote(crawler.searchWho), shellQuote(crawler.whoQuery), shellQuote(crawler.shortRefAlias), shellQuote(crawler.openHuman), shellQuote(crawler.openStderr), shellQuote(crawler.metadata), crawler.metadataExit, shellQuote(crawler.status), crawler.statusExit, shellQuote(crawler.doctor), crawler.doctorExit, shellQuote(crawler.search), crawler.searchExit, shellQuote(crawler.who), crawler.whoExit, shellQuote(crawler.open), crawler.openExit, crawler.openHumanExit, shellQuote(crawler.sync), crawler.syncExit)
	path := filepath.Join(dir, crawler.name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func shellBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func metadataJSON(id string) string {
	return fmt.Sprintf(`{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":%q,"display_name":%q}`, id, id)
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
