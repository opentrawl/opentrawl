//go:build darwin

package pluginsandbox

import (
	"context"
	"os/exec"
)

const macOSSandboxExecBinaryPath = "/usr/bin/sandbox-exec"

// CommandContext prepares an exec.Cmd that runs policy.ExecutablePath inside
// the compiled macOS Seatbelt sandbox profile.
func CommandContext(ctx context.Context, policy PluginSandboxPolicy, args ...string) (*exec.Cmd, error) {
	profile, err := RenderSeatbeltProfile(policy)
	if err != nil {
		return nil, err
	}
	canonicalExecutable, err := canonicalSandboxPath(policy.ExecutablePath, true)
	if err != nil {
		return nil, err
	}
	sandboxArgs := make([]string, 0, len(args)+4)
	sandboxArgs = append(sandboxArgs, "-p", profile, "--", canonicalExecutable)
	sandboxArgs = append(sandboxArgs, args...)
	return exec.CommandContext(ctx, macOSSandboxExecBinaryPath, sandboxArgs...), nil // #nosec G204 -- sandbox-exec and verified plugin path are host-controlled.
}
