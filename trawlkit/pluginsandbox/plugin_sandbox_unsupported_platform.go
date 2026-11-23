//go:build !darwin && !linux

package pluginsandbox

import (
	"context"
	"errors"
	"os/exec"
)

// CommandContext refuses to run plugins on platforms without a sandbox
// backend. OpenTrawl never runs a standalone plugin unsandboxed.
func CommandContext(context.Context, PluginSandboxPolicy, ...string) (*exec.Cmd, error) {
	return nil, errors.New("standalone plugin sandboxing is implemented only on macOS and Linux")
}
