package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// agentArchiveService answers read-only agent and remote app requests for
// trawl mcp. It finds the installed trawlers once, at start, and runs each
// request restricted to the requesting token's allowed sources.
type agentArchiveService struct {
	stateRoot                                      string
	registeredTrawlerManifestSnapshotAtServerStart registeredTrawlerManifestSnapshot
	installedTrawlersDiscoveredAtServerStart       []InstalledTrawler
	tailscaleMagicDNSHostName                      string
	tailscaleIPv4Address                           string
}

func newAgentArchiveService(ctx context.Context, stateRoot string) *agentArchiveService {
	snapshot := buildRegisteredTrawlerManifestSnapshotForContext(ctx, stateRoot, true)
	dnsName, ipv4 := detectTailscaleIdentity(ctx)
	return &agentArchiveService{
		stateRoot: stateRoot,
		registeredTrawlerManifestSnapshotAtServerStart: snapshot,
		installedTrawlersDiscoveredAtServerStart:       snapshot.installedTrawlers,
		tailscaleMagicDNSHostName:                      dnsName,
		tailscaleIPv4Address:                           ipv4,
	}
}

// newAgentRequestRuntime builds a scoped Runtime for one MCP or remote archive
// request.
func (service *agentArchiveService) newAgentRequestRuntime(ctx context.Context, scope agentSourceScope) *Runtime {
	return &Runtime{
		ctx: withAgentServingContext(ctx, agentServingContext{
			installedTrawlersDiscoveredAtServerStart: service.installedTrawlersDiscoveredAtServerStart,
			agentSourceScope:                         scope,
		}),
		output:    render.NewTrawlCommandOutput(&bytes.Buffer{}, &bytes.Buffer{}, "trawl"),
		root:      &CLI{},
		now:       time.Now,
		timeout:   registeredTrawlerCommandTimeout,
		stateRoot: service.stateRoot,
	}
}

// detectTailscaleMagicDNSHostName returns this machine's MagicDNS name, such
// as mac-mini.tailnet.ts.net, or "" when Tailscale is not running.
func detectTailscaleMagicDNSHostName(ctx context.Context) string {
	dnsName, _ := detectTailscaleIdentity(ctx)
	return dnsName
}

// detectTailscaleIdentity returns this machine's Tailscale MagicDNS host name
// and its Tailscale CGNAT IPv4 address (100.64.0.0/10), or empty strings when
// Tailscale is not running.
func detectTailscaleIdentity(ctx context.Context) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, tailscaleExecutable := range []string{"tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"} {
		encoded, err := exec.CommandContext(ctx, tailscaleExecutable, "status", "--json").Output() // #nosec G204 -- fixed executable names.
		if err != nil {
			continue
		}
		var tailscaleStatus struct {
			Self struct {
				DNSName      string   `json:"DNSName"`
				TailscaleIPs []string `json:"TailscaleIPs"`
			} `json:"Self"`
		}
		if json.Unmarshal(encoded, &tailscaleStatus) != nil {
			continue
		}
		dnsName := strings.TrimSuffix(tailscaleStatus.Self.DNSName, ".")
		var tailscaleIPv4 string
		for _, rawIP := range tailscaleStatus.Self.TailscaleIPs {
			if isTailscaleCGNATIPv4String(rawIP) {
				tailscaleIPv4 = rawIP
				break
			}
		}
		if dnsName != "" || tailscaleIPv4 != "" {
			return dnsName, tailscaleIPv4
		}
	}
	return "", ""
}

func isTailscaleCGNATIPv4String(raw string) bool {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 4 {
		return false
	}
	var octets [4]int
	for i, part := range parts {
		if len(part) == 0 || len(part) > 3 {
			return false
		}
		value := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
			value = value*10 + int(r-'0')
		}
		if value > 255 {
			return false
		}
		octets[i] = value
	}
	return octets[0] == 100 && octets[1] >= 64 && octets[1] <= 127
}
