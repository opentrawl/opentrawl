//go:build !darwin

package pluginsandbox

import (
	"context"
	"errors"
	"os/exec"
)

// CommandContext returns an error on platforms without a Seatbelt sandbox
// implementation until a native OS sandbox backend is added for that platform.
func CommandContext(context.Context, PluginSandboxPolicy, ...string) (*exec.Cmd, error) {
	return nil, errors.New("standalone plugin sandboxing is currently implemented only on macOS")
}
