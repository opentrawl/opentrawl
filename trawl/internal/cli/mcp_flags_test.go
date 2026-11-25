package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func TestMcpAndTokenCreateHelpHideRelayFlagsWhileStillParsingThem(t *testing.T) {
	renderHelp := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		exitCode := ExecuteWithTrawlInvocationDisplay(args, &stdout, &stderr, "trawl")
		if exitCode != 0 {
			t.Fatalf("Execute(%v) exited %d, stderr=%q", args, exitCode, stderr.String())
		}
		return stdout.String()
	}

	mcpHelp := renderHelp("mcp", "--help")
	for _, hiddenFlag := range []string{"--relay", "--pair", "--pair-window", "--relay-server", "--relay-domain"} {
		if strings.Contains(mcpHelp, hiddenFlag) {
			t.Fatalf("trawl mcp --help still shows hidden flag %s:\n%s", hiddenFlag, mcpHelp)
		}
	}
	for _, publicFlag := range []string{"--sources", "--http", "--tailscale", "--listen"} {
		if !strings.Contains(mcpHelp, publicFlag) {
			t.Fatalf("trawl mcp --help missing public flag %s:\n%s", publicFlag, mcpHelp)
		}
	}

	tokenCreateHelp := renderHelp("token", "create", "--help")
	if strings.Contains(tokenCreateHelp, "--relay") {
		t.Fatalf("trawl token create --help still shows hidden --relay flag:\n%s", tokenCreateHelp)
	}

	kongVars := kong.Vars{
		"on_or_after_date_option_description":  render.OnOrAfterDateOptionDescription,
		"on_or_before_date_option_description": render.OnOrBeforeDateOptionDescription,
	}

	var parsedCLI CLI
	parser, err := kong.New(&parsedCLI, kong.Name("trawl"), kong.Writers(io.Discard, io.Discard), kongVars)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{
		"mcp", "--relay", "--pair", "--pair-window", "10m",
		"--relay-server", "127.0.0.1:7000", "--relay-domain", "mcp.opentrawl.sh",
	}); err != nil {
		t.Fatalf("parse hidden mcp relay flags: %v", err)
	}
	if !parsedCLI.Mcp.Relay || !parsedCLI.Mcp.Pair || parsedCLI.Mcp.PairWindow != 10*time.Minute {
		t.Fatalf("hidden mcp relay flags did not bind as expected: %+v", parsedCLI.Mcp)
	}

	var tokenCLI CLI
	tokenParser, err := kong.New(&tokenCLI, kong.Name("trawl"), kong.Writers(io.Discard, io.Discard), kongVars)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := tokenParser.Parse([]string{"token", "create", "agent", "--relay"}); err != nil {
		t.Fatalf("parse hidden token create --relay: %v", err)
	}
	if !tokenCLI.Token.Create.Relay {
		t.Fatalf("hidden token create --relay did not bind")
	}
}
