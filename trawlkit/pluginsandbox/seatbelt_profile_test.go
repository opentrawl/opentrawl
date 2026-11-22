//go:build darwin

package pluginsandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMacOSSeatbeltBlocksUnauthorizedReadsWritesAndProtectedConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	allowedDir := filepath.Join(tempDir, "allowed")
	blockedDir := filepath.Join(tempDir, "blocked")
	if err := os.MkdirAll(allowedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blockedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(blockedDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(allowedDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("read-only"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Reading outside allowed directories must fail under Seatbelt.
	readBlockedCmd, err := CommandContext(ctx, PluginSandboxPolicy{
		ExecutablePath:         "/bin/cat",
		ReadOnlyDirectoryPaths: []string{allowedDir},
	}, secretPath)
	if err != nil {
		t.Fatalf("CommandContext(cat blocked): %v", err)
	}
	if err := readBlockedCmd.Run(); err == nil {
		t.Fatalf("expected Seatbelt to deny reading %s", secretPath)
	}

	// 2. Reading inside allowed directory must succeed under Seatbelt.
	readAllowedCmd, err := CommandContext(ctx, PluginSandboxPolicy{
		ExecutablePath:         "/bin/cat",
		ReadOnlyDirectoryPaths: []string{allowedDir},
	}, configPath)
	if err != nil {
		t.Fatalf("CommandContext(cat allowed): %v", err)
	}
	if out, err := readAllowedCmd.CombinedOutput(); err != nil {
		t.Fatalf("expected Seatbelt to allow reading %s: %v (%s)", configPath, err, string(out))
	}

	// 3. Writing to a file marked in DeniedWriteFilePaths inside a writable dir must fail.
	writeDeniedCmd, err := CommandContext(ctx, PluginSandboxPolicy{
		ExecutablePath:         "/usr/bin/touch",
		WritableDirectoryPaths: []string{allowedDir},
		DeniedWriteFilePaths:   []string{configPath},
	}, configPath)
	if err != nil {
		t.Fatalf("CommandContext(touch config): %v", err)
	}
	if err := writeDeniedCmd.Run(); err == nil {
		t.Fatalf("expected Seatbelt to deny writing protected config file %s", configPath)
	}

	// 4. Writing a sibling file inside the writable dir must succeed.
	siblingPath := filepath.Join(allowedDir, "archive.db")
	writeAllowedCmd, err := CommandContext(ctx, PluginSandboxPolicy{
		ExecutablePath:         "/usr/bin/touch",
		WritableDirectoryPaths: []string{allowedDir},
		DeniedWriteFilePaths:   []string{configPath},
	}, siblingPath)
	if err != nil {
		t.Fatalf("CommandContext(touch archive): %v", err)
	}
	if out, err := writeAllowedCmd.CombinedOutput(); err != nil {
		t.Fatalf("expected Seatbelt to allow writing %s: %v (%s)", siblingPath, err, string(out))
	}
}
