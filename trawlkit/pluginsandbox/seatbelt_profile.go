// Package pluginsandbox compiles and enforces per-invocation macOS Seatbelt
// sandboxes for standalone OpenTrawl plugin executables.
package pluginsandbox

import (
	"debug/macho"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PluginSandboxPolicy declares the exact minimum filesystem and network
// permissions for one standalone plugin invocation.
type PluginSandboxPolicy struct {
	ExecutablePath              string
	WritableDirectoryPaths      []string
	ReadOnlyDirectoryPaths      []string
	ReadOnlyFilePaths           []string
	DeniedWriteFilePaths        []string
	LoopbackProxyPort           int
	AllowNetworkTrustEvaluation bool
}

// RenderSeatbeltProfile compiles a strict (deny default) macOS Seatbelt
// profile matching policy.
func RenderSeatbeltProfile(policy PluginSandboxPolicy) (string, error) {
	executablePath, err := canonicalSandboxPath(policy.ExecutablePath, true)
	if err != nil {
		return "", fmt.Errorf("canonicalize plugin executable path: %w", err)
	}

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(import \"dyld-support.sb\")\n")
	b.WriteString("(allow process-exec (literal ")
	b.WriteString(seatbeltQuotedString(executablePath))
	b.WriteString("))\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow file-read-metadata)\n")
	b.WriteString("(allow file-read*\n")
	b.WriteString("  (literal ")
	b.WriteString(seatbeltQuotedString(executablePath))
	b.WriteString(")\n")
	b.WriteString("  (subpath \"/usr/lib\")\n")
	b.WriteString("  (subpath \"/System/Library\")\n")
	b.WriteString("  (literal \"/dev/null\")\n")
	b.WriteString("  (literal \"/dev/urandom\")\n")
	b.WriteString("  (literal \"/dev/random\")\n")
	b.WriteString("  (literal \"/private/etc/localtime\")\n")
	b.WriteString("  (subpath \"/private/var/db/timezone\")\n")
	b.WriteString("  (subpath \"/usr/share/zoneinfo\")\n")
	b.WriteString("  (subpath \"/usr/share/icu\")\n")
	b.WriteString("  (subpath \"/private/etc/ssl\")\n")
	if executableLinksNixStoreLibraries(executablePath) {
		b.WriteString("  (subpath \"/nix/store\")\n")
	}
	b.WriteString(")\n")
	b.WriteString("(allow file-write-data (literal \"/dev/null\"))\n")

	if err := appendReadOnlyPathRules(&b, policy.ReadOnlyDirectoryPaths, policy.ReadOnlyFilePaths); err != nil {
		return "", err
	}
	if err := appendWritableDirectoryRules(&b, policy.WritableDirectoryPaths); err != nil {
		return "", err
	}
	if err := appendDeniedWriteFileRules(&b, policy.DeniedWriteFilePaths); err != nil {
		return "", err
	}
	if policy.LoopbackProxyPort > 0 {
		if policy.LoopbackProxyPort > 65535 {
			return "", fmt.Errorf("invalid loopback proxy port %d", policy.LoopbackProxyPort)
		}
		b.WriteString("(allow network-outbound (remote ip \"localhost:")
		b.WriteString(strconv.Itoa(policy.LoopbackProxyPort))
		b.WriteString("\"))\n")
		if policy.AllowNetworkTrustEvaluation {
			b.WriteString("(allow mach-lookup (global-name \"com.apple.trustd.agent\"))\n")
		}
	}
	return b.String(), nil
}

func appendReadOnlyPathRules(b *strings.Builder, directories []string, files []string) error {
	for _, rawDir := range directories {
		trimmed := strings.TrimSpace(rawDir)
		if trimmed == "" {
			continue
		}
		canonical, err := canonicalSandboxPath(trimmed, false)
		if err != nil {
			return fmt.Errorf("read-only directory %q: %w", trimmed, err)
		}
		b.WriteString("(allow file-read* (subpath ")
		b.WriteString(seatbeltQuotedString(canonical))
		b.WriteString("))\n")
	}
	for _, rawFile := range files {
		trimmed := strings.TrimSpace(rawFile)
		if trimmed == "" {
			continue
		}
		canonical, err := canonicalSandboxPath(trimmed, false)
		if err != nil {
			return fmt.Errorf("read-only file %q: %w", trimmed, err)
		}
		b.WriteString("(allow file-read* (literal ")
		b.WriteString(seatbeltQuotedString(canonical))
		b.WriteString("))\n")
	}
	return nil
}

func appendWritableDirectoryRules(b *strings.Builder, directories []string) error {
	for _, rawDir := range directories {
		trimmed := strings.TrimSpace(rawDir)
		if trimmed == "" {
			continue
		}
		canonical, err := canonicalSandboxPath(trimmed, false)
		if err != nil {
			return fmt.Errorf("writable directory %q: %w", trimmed, err)
		}
		b.WriteString("(allow file-read* file-write* (subpath ")
		b.WriteString(seatbeltQuotedString(canonical))
		b.WriteString("))\n")
	}
	return nil
}

func appendDeniedWriteFileRules(b *strings.Builder, files []string) error {
	for _, rawFile := range files {
		trimmed := strings.TrimSpace(rawFile)
		if trimmed == "" {
			continue
		}
		canonical, err := canonicalSandboxPath(trimmed, false)
		if err != nil {
			return fmt.Errorf("denied-write file %q: %w", trimmed, err)
		}
		// In macOS Seatbelt SBPL, the last matching rule takes precedence, so
		// emitting deny rules after directory write grants keeps host-owned
		// configuration read-only to the plugin.
		b.WriteString("(deny file-write* (literal ")
		b.WriteString(seatbeltQuotedString(canonical))
		b.WriteString("))\n")
	}
	return nil
}

func canonicalSandboxPath(rawPath string, mustExist bool) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", errors.New("path is empty")
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return resolved, nil
	}
	if mustExist || !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// Resolve symlinks on the nearest existing ancestor (for example /var -> /private/var).
	parent := filepath.Dir(absPath)
	base := filepath.Base(absPath)
	if parent == absPath {
		return absPath, nil
	}
	resolvedParent, parentErr := canonicalSandboxPath(parent, false)
	if parentErr != nil {
		return absPath, nil
	}
	return filepath.Join(resolvedParent, base), nil
}

func executableLinksNixStoreLibraries(executablePath string) bool {
	machoFile, err := macho.Open(executablePath)
	if err != nil {
		return false
	}
	defer func() { _ = machoFile.Close() }()
	libraries, err := machoFile.ImportedLibraries()
	if err != nil {
		return false
	}
	for _, library := range libraries {
		if strings.HasPrefix(library, "/nix/store/") {
			return true
		}
	}
	return false
}

func seatbeltQuotedString(text string) string {
	escaped := strings.ReplaceAll(text, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
