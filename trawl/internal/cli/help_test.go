package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bare `trawl` (and `trawl --help`) used to hardcode a five-source prose
// sentence. The source list now comes from the explicit crawlkit
// registrations, so every registered crawler shows up.
func TestSourcesLineListsEveryInstalledCrawler(t *testing.T) {
	imsg := fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`}
	bird := fakeCrawler{name: "birdcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"twitter","display_name":"X"}`}
	binDir := writeFakeCrawlers(t, imsg, bird)
	t.Setenv("PATH", binDir)

	line := sourcesLine(context.Background())

	if !strings.Contains(line, "iMessage") {
		t.Errorf("sourcesLine() = %q, want iMessage", line)
	}
	if !strings.Contains(line, "X") {
		t.Errorf("sourcesLine() = %q, want X", line)
	}
}

func TestSourcesLineDegradesHonestlyWithNoCrawlersInstalled(t *testing.T) {
	oldFactories := crawlerFactories
	crawlerFactories = nil
	t.Cleanup(func() { crawlerFactories = oldFactories })

	line := sourcesLine(context.Background())

	if !strings.Contains(line, "No crawlers are registered") {
		t.Errorf("sourcesLine() = %q, want an honest no-crawlers message", line)
	}
}

func TestBareTrawlHelpListsAllInstalledSources(t *testing.T) {
	imsg := fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`}
	bird := fakeCrawler{name: "birdcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"twitter","display_name":"X"}`}
	photo := fakeCrawler{name: "photoscrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"photos","display_name":"Photos"}`}
	binDir := writeFakeCrawlers(t, imsg, bird, photo)
	t.Setenv("PATH", binDir)

	stdout, _, exitCode := runCLI(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{"iMessage", "X", "Photos"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("bare trawl help missing %q:\n%s", want, stdout)
		}
	}
}

// A crawler that is not registered must simply be absent from the listing:
// no placeholder, no error line naming it.
func TestBareTrawlHelpOmitsUnregisteredCrawler(t *testing.T) {
	imsg := fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`}
	binDir := writeFakeCrawlers(t, imsg)
	t.Setenv("PATH", binDir)

	stdout, _, exitCode := runCLI(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if strings.Contains(stdout, "birdcrawl") {
		t.Errorf("bare trawl help named an uninstalled crawler:\n%s", stdout)
	}
	if !strings.Contains(stdout, "iMessage") {
		t.Errorf("bare trawl help missing the one installed crawler:\n%s", stdout)
	}
}

// `trawl -h` must render the same root help as `trawl --help` — kong's
// default help flag registers short 'h', and the first cut of wantsHelp
// (then unqualified) missed it: `trawl -h` rendered root help with the
// source paragraph silently missing. A global flag (--json, -v) sitting
// alongside -h/--help, in either order, must not defeat the match either
// — that was a second real bug caught on re-review.
func TestShortHelpFlagListsSources(t *testing.T) {
	imsg := fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`}
	binDir := writeFakeCrawlers(t, imsg)
	t.Setenv("PATH", binDir)

	for _, args := range [][]string{
		{"-h"},
		{"--json", "-h"},
		{"-v", "-h"},
		{"-h", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, exitCode := runCLI(t, args...)

			if exitCode != 0 {
				t.Fatalf("exit code = %d, want 0", exitCode)
			}
			if !strings.Contains(stdout, "iMessage") {
				t.Errorf("trawl %s missing the sources line:\n%s", strings.Join(args, " "), stdout)
			}
		})
	}
}

// Subcommand help must not pay the discovery cost or print the source
// list — only root help does.
func TestSubcommandHelpDoesNotListSources(t *testing.T) {
	imsg := fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`}
	binDir := writeFakeCrawlers(t, imsg)
	t.Setenv("PATH", binDir)
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("TRAWL_FAKE_LOG", logPath)

	stdout, _, exitCode := runCLI(t, "search", "--help")

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if strings.Contains(stdout, "Sources go by id or surface name") {
		t.Errorf("subcommand help printed the root sources line:\n%s", stdout)
	}
	if _, err := os.Stat(logPath); err == nil {
		t.Errorf("subcommand help ran crawler discovery (fake crawler was invoked), defeating the whole point of deferring it")
	}
}
