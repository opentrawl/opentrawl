package pluginruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	"google.golang.org/protobuf/proto"
)

func TestParseSelfHostedHostApprovalSpecAcceptsValidCIDRListAndRejectsMalformedInput(t *testing.T) {
	approval, err := ParseSelfHostedHostApprovalSpec("where.example.internal:443=100.64.0.0/10,192.168.1.0/24")
	if err != nil {
		t.Fatalf("ParseSelfHostedHostApprovalSpec returned unexpected error: %v", err)
	}
	if approval.GetHostAndPort() != "where.example.internal:443" {
		t.Fatalf("got host %q, want where.example.internal:443", approval.GetHostAndPort())
	}
	if len(approval.GetAllowedIpPrefixes()) != 2 ||
		approval.GetAllowedIpPrefixes()[0] != "100.64.0.0/10" ||
		approval.GetAllowedIpPrefixes()[1] != "192.168.1.0/24" {
		t.Fatalf("unexpected prefixes: %v", approval.GetAllowedIpPrefixes())
	}

	for _, invalid := range []string{
		"",
		"where.example.internal:443",
		"where.example.internal:443=",
		"=100.64.0.0/10",
		"where.example.internal=100.64.0.0/10",
		"where.example.internal:443=not-a-cidr",
	} {
		if _, err := ParseSelfHostedHostApprovalSpec(invalid); err == nil {
			t.Errorf("expected error for malformed self-hosted spec %q, got nil", invalid)
		}
	}
}

func TestStagedPluginTrustRecordRejectsBinaryHashMismatchAndRemovalPreservesArchive(t *testing.T) {
	stateRoot := t.TempDir()
	pluginID := "demo-plugin"
	pluginDir := filepath.Join(stateRoot, "plugins", pluginID)
	archiveDir := filepath.Join(stateRoot, pluginID)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}

	stagedBinaryPath := filepath.Join(pluginDir, stagedPluginBinaryFilename)
	if err := os.WriteFile(stagedBinaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	archiveFile := filepath.Join(archiveDir, pluginID+".db")
	if err := os.WriteFile(archiveFile, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	expectedHash, err := computeFileSHA256Hex(stagedBinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(pluginDir, pluginTrustRecordFilename)
	trustBytes, err := proto.Marshal(&plugincontract.PluginTrustRecord{
		PluginIdentifier:        pluginID,
		ApprovedBinarySha256Hex: expectedHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustPath, trustBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadAndVerifyPluginTrustRecord(trustPath, pluginID, expectedHash); err != nil {
		t.Fatalf("expected valid trust record to verify, got: %v", err)
	}

	if err := os.WriteFile(stagedBinaryPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tamperedHash, err := computeFileSHA256Hex(stagedBinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadAndVerifyPluginTrustRecord(trustPath, pluginID, tamperedHash); err == nil || !strings.Contains(err.Error(), "binary hash") {
		t.Fatalf("expected binary hash mismatch error, got: %v", err)
	}

	removed, err := RemoveStagedExternalPlugin(stateRoot, pluginID)
	if err != nil {
		t.Fatalf("RemoveStagedExternalPlugin failed: %v", err)
	}
	if !removed {
		t.Fatalf("expected RemoveStagedExternalPlugin to report removed=true")
	}
	if _, err := os.Stat(stagedBinaryPath); !os.IsNotExist(err) {
		t.Fatalf("expected staged binary to be removed, stat err: %v", err)
	}
	if _, err := os.Stat(archiveFile); err != nil {
		t.Fatalf("expected plugin archive file to be preserved, stat err: %v", err)
	}
}

func TestBundledReadPathResolutionHandlesDirectoriesFilesAndConfigFields(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "group-container")
	keyFile := filepath.Join(root, "private.pem")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, dirs := resolveExistingBundledReadPaths([]string{sourceDir, keyFile, filepath.Join(root, "missing")})
	if len(files) != 1 || files[0] != keyFile {
		t.Fatalf("unexpected resolved files: %v", files)
	}
	if len(dirs) != 1 || dirs[0] != sourceDir {
		t.Fatalf("unexpected resolved dirs: %v", dirs)
	}

	configPath := filepath.Join(root, "config.toml")
	configBody := "source = \"" + sourceDir + "\"\n[enable_banking]\nprivate_key_file = \"" + keyFile + "\"\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgFiles, cfgDirs := extractBundledConfigReadPaths(configPath, []string{"source", "enable_banking.private_key_file"})
	if len(cfgFiles) != 1 || cfgFiles[0] != keyFile {
		t.Fatalf("unexpected config files: %v", cfgFiles)
	}
	if len(cfgDirs) != 1 || cfgDirs[0] != sourceDir {
		t.Fatalf("unexpected config dirs: %v", cfgDirs)
	}
}
