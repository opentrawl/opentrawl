package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// agentArchiveService answers read-only agent requests for trawl mcp. It finds
// the installed trawlers once, at start, and runs each tool call as the same
// trawl command a person would type, restricted to the requesting agent's
// allowed sources.
type agentArchiveService struct {
	stateRoot                                string
	installedTrawlersDiscoveredAtServerStart []InstalledTrawler
	tailscaleMagicDNSHostName                string
}

func newAgentArchiveService(ctx context.Context, stateRoot string) *agentArchiveService {
	return &agentArchiveService{
		stateRoot:                                stateRoot,
		installedTrawlersDiscoveredAtServerStart: buildRegisteredTrawlerManifestSnapshotForContext(ctx, stateRoot, false).installedTrawlers,
		tailscaleMagicDNSHostName:                detectTailscaleMagicDNSHostName(ctx),
	}
}

// agentRequestRuntime is one agent request: a Runtime whose discovery is
// scoped, and whose output library writes into buffers instead of a terminal.
type agentRequestRuntime struct {
	runtime        *Runtime
	standardOutput *bytes.Buffer
	standardError  *bytes.Buffer
}

func (service *agentArchiveService) newAgentRequestRuntime(ctx context.Context, scope agentSourceScope) agentRequestRuntime {
	standardOutput, standardError := &bytes.Buffer{}, &bytes.Buffer{}
	return agentRequestRuntime{
		runtime: &Runtime{
			ctx: withAgentServingContext(ctx, agentServingContext{
				installedTrawlersDiscoveredAtServerStart: service.installedTrawlersDiscoveredAtServerStart,
				agentSourceScope:                         scope,
			}),
			output:    render.NewTrawlCommandOutput(standardOutput, standardError, "trawl"),
			root:      &CLI{},
			now:       time.Now,
			timeout:   registeredTrawlerCommandTimeout,
			stateRoot: service.stateRoot,
		},
		standardOutput: standardOutput,
		standardError:  standardError,
	}
}

// runTrawlCommandForAgent runs one trawl command and returns exactly what the
// output library wrote, and whether the command failed.
func (service *agentArchiveService) runTrawlCommandForAgent(ctx context.Context, scope agentSourceScope, run func(*Runtime) error) (string, bool) {
	request := service.newAgentRequestRuntime(ctx, scope)
	err := run(request.runtime)
	failed := false
	if err != nil {
		var outcome commandOutcomeAlreadyShown
		if errors.As(err, &outcome) {
			failed = outcome.processExitCode != 0
		} else {
			failed = true
			if failure, worded := render.AsCommandFailure(err); worded {
				_ = request.runtime.output.WriteCommandFailure(failure)
			} else {
				_ = request.runtime.output.WriteCommandFailure(err)
			}
		}
	}
	text := strings.TrimRight(request.standardOutput.String(), "\n")
	if errorText := strings.TrimSpace(request.standardError.String()); errorText != "" {
		if text != "" {
			text += "\n\n"
		}
		text += errorText
	}
	return text, failed
}

// detectTailscaleMagicDNSHostName returns this machine's MagicDNS name, such
// as mac-mini.tailnet.ts.net, or "" when Tailscale is not running.
func detectTailscaleMagicDNSHostName(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, tailscaleExecutable := range []string{"tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"} {
		encoded, err := exec.CommandContext(ctx, tailscaleExecutable, "status", "--json").Output() // #nosec G204 -- fixed executable names.
		if err != nil {
			continue
		}
		var tailscaleStatus struct {
			Self struct {
				DNSName string `json:"DNSName"`
			} `json:"Self"`
		}
		if json.Unmarshal(encoded, &tailscaleStatus) == nil && tailscaleStatus.Self.DNSName != "" {
			return strings.TrimSuffix(tailscaleStatus.Self.DNSName, ".")
		}
	}
	return ""
}
