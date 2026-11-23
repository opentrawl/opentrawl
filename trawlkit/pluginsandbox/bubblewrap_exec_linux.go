//go:build linux

package pluginsandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// linuxSystemPathsReadableInsidePluginSandbox are the read-only system paths a
// dynamically linked Go plugin needs: the loader and libc, time zones and TLS
// roots. /nix/store and /run/current-system cover NixOS.
var linuxSystemPathsReadableInsidePluginSandbox = []string{
	"/usr", "/lib", "/lib64", "/bin", "/etc/ssl", "/etc/pki", "/etc/ca-certificates",
	"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/localtime", "/etc/zoneinfo",
	"/nix/store", "/run/current-system",
}

// CommandContext prepares an exec.Cmd that runs policy.ExecutablePath inside a
// bubblewrap (bwrap) sandbox: new user, mount, PID, IPC and UTS namespaces, an
// empty root with only the system paths above, the plugin's declared
// directories and files, and private /proc, /dev and /tmp.
//
// The network namespace is private unless the plugin talks to the host's
// loopback network proxy (LoopbackProxyPort). A private network namespace has
// its own loopback, so it cannot reach that proxy. In that case the plugin
// shares the host network namespace and relies on the proxy for host
// allow-listing, which is weaker isolation than macOS Seatbelt gives.
//
// Without bwrap on PATH there is no sandbox, so the plugin does not run.
func CommandContext(ctx context.Context, policy PluginSandboxPolicy, args ...string) (*exec.Cmd, error) {
	bubblewrapExecutablePath, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, errors.New("standalone plugins on Linux run inside bubblewrap; install bwrap (bubblewrap) to run them")
	}
	executablePath, err := canonicalSandboxPath(policy.ExecutablePath, true)
	if err != nil {
		return nil, err
	}
	bubblewrapArguments := []string{
		"--die-with-parent", "--new-session",
		"--unshare-user", "--unshare-ipc", "--unshare-pid", "--unshare-uts", "--unshare-cgroup-try",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
	}
	if policy.LoopbackProxyPort <= 0 {
		bubblewrapArguments = append(bubblewrapArguments, "--unshare-net")
	}
	for _, systemPath := range linuxSystemPathsReadableInsidePluginSandbox {
		bubblewrapArguments = append(bubblewrapArguments, "--ro-bind-try", systemPath, systemPath)
	}
	bubblewrapArguments = append(bubblewrapArguments, "--ro-bind", executablePath, executablePath)
	for _, readOnlyPaths := range [][]string{policy.ReadOnlyDirectoryPaths, policy.ReadOnlyFilePaths} {
		for _, rawPath := range readOnlyPaths {
			readOnlyPath, err := canonicalSandboxPath(rawPath, false)
			if err != nil {
				return nil, err
			}
			bubblewrapArguments = append(bubblewrapArguments, "--ro-bind-try", readOnlyPath, readOnlyPath)
		}
	}
	for _, rawPath := range policy.WritableDirectoryPaths {
		writablePath, err := canonicalSandboxPath(rawPath, false)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(writablePath, 0o700); err != nil {
			return nil, err
		}
		bubblewrapArguments = append(bubblewrapArguments, "--bind", writablePath, writablePath)
	}
	for _, rawPath := range policy.DeniedWriteFilePaths {
		deniedPath, err := canonicalSandboxPath(rawPath, false)
		if err != nil {
			return nil, err
		}
		bubblewrapArguments = append(bubblewrapArguments, "--ro-bind-try", deniedPath, deniedPath)
	}
	bubblewrapArguments = append(bubblewrapArguments, "--", executablePath)
	bubblewrapArguments = append(bubblewrapArguments, args...)
	return exec.CommandContext(ctx, bubblewrapExecutablePath, bubblewrapArguments...), nil // #nosec G204 -- bwrap and the verified plugin path are host-controlled.
}
