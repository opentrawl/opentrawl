package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// binaryIDs are the internal module names that must never leak into any
// human surface. The front door and --help render surface names
// only, so grepping the output for one of these is a defect.
var binaryIDs = []string{"imsgcrawl", "telecrawl", "wacrawl", "gogcrawl", "calcrawl", "birdcrawl", "clawdex", "photoscrawl"}

func liveSourceFakes() []fakeCrawler {
	return []fakeCrawler{
		{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`},
		{name: "birdcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"twitter","surface":"x","display_name":"Twitter (X)","aliases":["twitter"]}`},
		{name: "photoscrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"photos","display_name":"Photos"}`},
	}
}

func TestBareFrontDoorRendersSyntheticManifestHeadlines(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{name: "alphacrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"alpha","display_name":"Alpha","binary":{"name":"alphacrawl"},"capabilities":["sync","search","open"],"headlines":["zeta","alpha","middle"]}`},
		fakeCrawler{name: "emptycrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"empty","display_name":"Empty","binary":{"name":"emptycrawl"}}`},
		fakeCrawler{name: "betacrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"beta","display_name":"Beta","binary":{"name":"betacrawl"},"headlines":["only"]}`},
	)
	t.Setenv("PATH", binDir)

	stdout, _, exitCode := runCLI(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	sources := outputSection(stdout, "Sources:")
	want := strings.Join([]string{
		"Sources:",
		"  Alpha     zeta · alpha · middle",
		"  Empty",
		"  Beta      only",
	}, "\n")
	if sources != want {
		t.Fatalf("sources block:\n%s\nwant:\n%s", sources, want)
	}
	for _, forbidden := range []string{"search", "sync", "status"} {
		if strings.Contains(sources, forbidden) {
			t.Errorf("sources block included universal verb %q:\n%s", forbidden, sources)
		}
	}
	if !strings.Contains(stdout, "Start here:") {
		t.Errorf("bare trawl missing Start here:\n%s", stdout)
	}
}

func TestBareFrontDoorMatchesBlessedDeclarations(t *testing.T) {
	stdout, _, exitCode := runCLI(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	want := `Sources:
  Messages                  chats
  WhatsApp                  chats · groups
  Telegram                  chats · folders · topics
  Gmail                     emails
  Calendar                  events · calendars
  Contacts                  people
  Photos                    photos
  Twitter (X) (twitter)     tweets · bookmarks · likes · mentions
  Notes                     notes · folders · versions

Start here:
  trawl status                 every source, and how fresh
  trawl search "boat trip"     all sources at once, newest first
  trawl chats --with anna      conversations across every messaging source
  trawl telegram               everything telegram can do
`
	if stdout != want {
		t.Fatalf("bare trawl output:\n%s\nwant:\n%s", stdout, want)
	}
}

// Bare `trawl` is the split-out front door: a short page that renders the
// live Sources block and never leaks an internal binary id.
func TestBareFrontDoorIsShortAndShowsSourcesBlock(t *testing.T) {
	binDir := writeFakeCrawlers(t, liveSourceFakes()...)
	t.Setenv("PATH", binDir)

	stdout, _, exitCode := runCLI(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	lines := strings.Count(strings.TrimRight(stdout, "\n"), "\n") + 1
	if lines > 20 {
		t.Errorf("bare trawl rendered %d lines, want 20 or fewer:\n%s", lines, stdout)
	}
	for _, want := range []string{"Sources:", "Start here:", "Twitter (X)", "Photos"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("bare trawl missing %q:\n%s", want, stdout)
		}
	}
	for _, id := range binaryIDs {
		if strings.Contains(stdout, id) {
			t.Errorf("bare trawl leaked internal binary id %q:\n%s", id, stdout)
		}
	}
}

// A person who types "imessage" or "twitter" must find the row,
// case-sensitively, without knowing the crawler's internal id.
func TestBareFrontDoorIsGreppableByTypedName(t *testing.T) {
	binDir := writeFakeCrawlers(t, liveSourceFakes()...)
	t.Setenv("PATH", binDir)

	stdout, _, exitCode := runCLI(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{"imessage", "twitter"} {
		found := false
		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no bare-trawl line contains %q:\n%s", want, stdout)
		}
	}
}

// `trawl --help` keeps the fuller generated page and gains the agents block:
// the ref grammar, the --json contract, and one worked search-to-open
// transcript. It still shows the Sources block and no internal binary ids.
func TestHelpShowsFullPageAndAgentsBlock(t *testing.T) {
	binDir := writeFakeCrawlers(t, liveSourceFakes()...)
	t.Setenv("PATH", binDir)

	stdout, _, exitCode := runCLI(t, "--help")

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{"Commands:", "Sources:", "Agents:", "source:kind/id", "imessage:msg/8842", "Use ordinary command output", "Use --json only when writing a script or pipeline", "chats"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("trawl --help missing %q:\n%s", want, stdout)
		}
	}
	for _, id := range binaryIDs {
		if strings.Contains(stdout, id) {
			t.Errorf("trawl --help leaked internal binary id %q:\n%s", id, stdout)
		}
	}
	for _, forbidden := range []string{"DOCTOR", "trawl doctor", "agents, prefer this"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("trawl --help exposed removed or machine-first guidance %q:\n%s", forbidden, stdout)
		}
	}
}

// A crawler that is not registered must simply be absent from the block: no
// placeholder, no error line naming it.
func TestFrontDoorOmitsUnregisteredCrawler(t *testing.T) {
	imsg := fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`}
	binDir := writeFakeCrawlers(t, imsg)
	t.Setenv("PATH", binDir)

	stdout, _, exitCode := runCLI(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if strings.Contains(stdout, "photos") || strings.Contains(stdout, "Twitter (X)") {
		t.Errorf("front door named an uninstalled crawler:\n%s", stdout)
	}
	if !strings.Contains(stdout, "imessage") {
		t.Errorf("front door missing the one installed crawler:\n%s", stdout)
	}
}

// `trawl -h` renders the same root help as `trawl --help` — the Sources
// block and agents block — even with a global flag alongside it.
func TestShortHelpFlagShowsSourcesBlock(t *testing.T) {
	binDir := writeFakeCrawlers(t, liveSourceFakes()...)
	t.Setenv("PATH", binDir)

	for _, args := range [][]string{{"-h"}, {"--json", "-h"}, {"-h", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, exitCode := runCLI(t, args...)

			if exitCode != 0 {
				t.Fatalf("exit code = %d, want 0", exitCode)
			}
			for _, want := range []string{"Sources:", "Agents:"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("trawl %s missing %q:\n%s", strings.Join(args, " "), want, stdout)
				}
			}
		})
	}
}

func outputSection(output, title string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var out []string
	inSection := false
	for _, line := range lines {
		if line == title {
			inSection = true
		}
		if !inSection {
			continue
		}
		if line == "" {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Subcommand help must not pay the discovery cost or repeat the root
// Sources/agents blocks — only root help does.
func TestSubcommandHelpDoesNotShowSourcesBlock(t *testing.T) {
	imsg := fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage"}`}
	binDir := writeFakeCrawlers(t, imsg)
	t.Setenv("PATH", binDir)
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("TRAWL_FAKE_LOG", logPath)

	stdout, _, exitCode := runCLI(t, "search", "--help")

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if strings.Contains(stdout, "Sources:") || strings.Contains(stdout, "Agents:") {
		t.Errorf("subcommand help printed the root Sources/agents blocks:\n%s", stdout)
	}
	if _, err := os.Stat(logPath); err == nil {
		t.Errorf("subcommand help ran crawler discovery, defeating the whole point of deferring it")
	}
}

// The block degrades honestly when no crawler is registered.
func TestFrontDoorDegradesWithNoCrawlers(t *testing.T) {
	oldFactories := crawlerFactories
	crawlerFactories = nil
	t.Cleanup(func() { crawlerFactories = oldFactories })

	if got := sourcesBlock(nil); !strings.Contains(got, "No crawlers are installed") {
		t.Errorf("sourcesBlock(nil) = %q, want an honest no-crawlers message", got)
	}
}

// birdcrawl declares the alias "twitter", so `trawl twitter` resolves the
// same source as the canonical `trawl x`.
func TestTwitterAliasResolvesSameSourceAsX(t *testing.T) {
	bird := fakeCrawler{name: "birdcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"twitter","surface":"x","display_name":"Twitter (X)","aliases":["twitter"]}`}
	binDir := writeFakeCrawlers(t, bird)
	t.Setenv("PATH", binDir)

	sources := discoverCrawlers(context.Background())
	canonical, okX := findSource(sources, "x")
	aliased, okTwitter := findSource(sources, "twitter")

	if !okX || !okTwitter {
		t.Fatalf("resolved x=%v twitter=%v, want both", okX, okTwitter)
	}
	if canonical.ID != aliased.ID {
		t.Errorf("trawl twitter resolved %q, trawl x resolved %q, want the same source", aliased.ID, canonical.ID)
	}
	if canonical.ID != "twitter" {
		t.Errorf("resolved source id = %q, want twitter", canonical.ID)
	}
}
