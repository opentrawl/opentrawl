package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/pluginproxy"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	"github.com/opentrawl/opentrawl/trawlkit/secrets"
	"google.golang.org/protobuf/proto"
)

// StageExternalPluginRequest defines the user-approved binary and capabilities
// to stage under <stateRoot>/plugins/<plugin_id>/.
type StageExternalPluginRequest struct {
	SourceExecutablePath       string
	ApprovedReadPaths          []string
	ApprovedNetworkHosts       []string
	ApprovedSelfHostedHosts    []*plugincontract.SelfHostedNetworkHostApproval
	ApprovedReadSecretKeys     []string
	ApprovedWriteSecretKeys    []string
	ApprovedAuthOrigins        []string
	ApprovedArchiveDirOverride string
}

// StagedExternalPluginOutcome describes one successfully staged external plugin
// and its verified manifest and trust record.
type StagedExternalPluginOutcome struct {
	PluginIdentifier string
	DisplayName      string
	StagedBinaryPath string
	TrustRecordPath  string
	ExecutableSHA256 string
	Manifest         *plugincontract.PluginManifestResponse
	TrustRecord      *plugincontract.PluginTrustRecord
}

// StageExternalPluginExecutable probes sourceExecutablePath under a zero-disk,
// zero-network Seatbelt sandbox, validates its identity and capability grants,
// stages an atomic copy at <stateRoot>/plugins/<id>/staged.bin, and writes the
// SHA-256-pinned host-owned trust.pb record.
func StageExternalPluginExecutable(
	ctx context.Context,
	stateRoot string,
	req StageExternalPluginRequest,
	reservedBuiltInIDs map[string]struct{},
) (StagedExternalPluginOutcome, error) {
	resolvedStateRoot, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return StagedExternalPluginOutcome{}, err
	}
	sourcePath := canonicalizeExecutablePath(req.SourceExecutablePath)
	if sourcePath == "" || !isExecutableRegularFile(sourcePath) {
		return StagedExternalPluginOutcome{}, fmt.Errorf("plugin executable %q does not exist or is not executable", req.SourceExecutablePath)
	}
	digestHex, err := computeFileSHA256Hex(sourcePath)
	if err != nil {
		return StagedExternalPluginOutcome{}, fmt.Errorf("hash plugin executable: %w", err)
	}
	manifestResp, err := loadOrProbePluginManifest(ctx, resolvedStateRoot, sourcePath, digestHex)
	if err != nil {
		return StagedExternalPluginOutcome{}, err
	}
	pluginID := trawlkit.RegisteredTrawlerIdentityText(manifestResp.GetRegisteredTrawlerManifest().GetRegisteredTrawler())
	if err := secrets.ValidatePluginIdentifier(pluginID); err != nil {
		return StagedExternalPluginOutcome{}, fmt.Errorf("invalid plugin identifier: %w", err)
	}
	if _, collides := reservedBuiltInIDs[pluginID]; collides {
		return StagedExternalPluginOutcome{}, fmt.Errorf("plugin identifier %q collides with a built-in trawler", pluginID)
	}

	trustRecord, err := buildValidatedPluginTrustRecord(pluginID, digestHex, req)
	if err != nil {
		return StagedExternalPluginOutcome{}, err
	}

	pluginDir := filepath.Join(resolvedStateRoot, "plugins", pluginID)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		return StagedExternalPluginOutcome{}, fmt.Errorf("create plugin directory: %w", err)
	}
	stagedPath := filepath.Join(pluginDir, stagedPluginBinaryFilename)
	if err := copyFileAtomically(sourcePath, stagedPath, 0o755); err != nil {
		return StagedExternalPluginOutcome{}, fmt.Errorf("stage plugin executable: %w", err)
	}
	// Re-hash staged copy so the trust record always pins the exact staged bytes.
	stagedDigestHex, err := computeFileSHA256Hex(stagedPath)
	if err != nil {
		return StagedExternalPluginOutcome{}, fmt.Errorf("hash staged plugin executable: %w", err)
	}
	trustRecord.ApprovedBinarySha256Hex = stagedDigestHex

	trustBytes, err := proto.Marshal(trustRecord)
	if err != nil {
		return StagedExternalPluginOutcome{}, fmt.Errorf("encode plugin trust record: %w", err)
	}
	trustPath := filepath.Join(pluginDir, pluginTrustRecordFilename)
	if err := writeBytesAtomically(trustPath, trustBytes, 0o600); err != nil {
		return StagedExternalPluginOutcome{}, fmt.Errorf("write plugin trust record: %w", err)
	}

	displayName := strings.TrimSpace(manifestResp.GetRegisteredTrawlerManifest().GetRegisteredTrawlerDisplayName())
	if displayName == "" {
		displayName = pluginID
	}
	return StagedExternalPluginOutcome{
		PluginIdentifier: pluginID,
		DisplayName:      displayName,
		StagedBinaryPath: stagedPath,
		TrustRecordPath:  trustPath,
		ExecutableSHA256: stagedDigestHex,
		Manifest:         manifestResp,
		TrustRecord:      trustRecord,
	}, nil
}

// RemoveStagedExternalPlugin removes a staged user plugin executable and trust
// record without deleting the plugin's archive directory.
func RemoveStagedExternalPlugin(stateRoot string, pluginIdentifier string) (bool, error) {
	resolvedStateRoot, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return false, err
	}
	pluginID := strings.TrimSpace(pluginIdentifier)
	if err := secrets.ValidatePluginIdentifier(pluginID); err != nil {
		return false, err
	}
	pluginDir := filepath.Join(resolvedStateRoot, "plugins", pluginID)
	removedAny := false
	for _, filename := range []string{stagedPluginBinaryFilename, pluginTrustRecordFilename} {
		target := filepath.Join(pluginDir, filename)
		if err := os.Remove(target); err == nil {
			removedAny = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return removedAny, err
		}
	}
	_ = os.Remove(pluginDir)
	return removedAny, nil
}

// ParseSelfHostedHostApprovalSpec parses "host:port=cidr1[,cidr2]" into a
// validated SelfHostedNetworkHostApproval protobuf message.
func ParseSelfHostedHostApprovalSpec(spec string) (*plugincontract.SelfHostedNetworkHostApproval, error) {
	rawHost, rawPrefixes, hasEq := strings.Cut(strings.TrimSpace(spec), "=")
	if !hasEq || strings.TrimSpace(rawHost) == "" || strings.TrimSpace(rawPrefixes) == "" {
		return nil, fmt.Errorf("self-hosted approval %q must use HOST:PORT=CIDR[,CIDR...]", spec)
	}
	normalizedHost, err := pluginproxy.NormalizeHostAndPort(rawHost)
	if err != nil {
		return nil, fmt.Errorf("self-hosted approval host %q: %w", rawHost, err)
	}
	rawList := strings.Split(rawPrefixes, ",")
	parsedPrefixes, err := pluginproxy.ParseIPPrefixes(rawList)
	if err != nil {
		return nil, fmt.Errorf("self-hosted approval prefixes for %q: %w", normalizedHost, err)
	}
	canonicalPrefixes := make([]string, 0, len(parsedPrefixes))
	for _, prefix := range parsedPrefixes {
		canonicalPrefixes = append(canonicalPrefixes, prefix.String())
	}
	return &plugincontract.SelfHostedNetworkHostApproval{
		HostAndPort:       normalizedHost,
		AllowedIpPrefixes: canonicalPrefixes,
	}, nil
}

func buildValidatedPluginTrustRecord(
	pluginID string,
	digestHex string,
	req StageExternalPluginRequest,
) (*plugincontract.PluginTrustRecord, error) {
	record := &plugincontract.PluginTrustRecord{
		PluginIdentifier:        pluginID,
		ApprovedBinarySha256Hex: digestHex,
	}
	for _, rawHost := range req.ApprovedNetworkHosts {
		normalized, err := pluginproxy.NormalizeHostAndPort(rawHost)
		if err != nil {
			return nil, fmt.Errorf("approved network host %q: %w", rawHost, err)
		}
		record.ApprovedNetworkHosts = append(record.ApprovedNetworkHosts, normalized)
	}
	for _, approval := range req.ApprovedSelfHostedHosts {
		if approval == nil {
			continue
		}
		normalizedHost, err := pluginproxy.NormalizeHostAndPort(approval.GetHostAndPort())
		if err != nil {
			return nil, fmt.Errorf("approved self-hosted host %q: %w", approval.GetHostAndPort(), err)
		}
		if _, err := pluginproxy.ParseIPPrefixes(approval.GetAllowedIpPrefixes()); err != nil {
			return nil, fmt.Errorf("approved self-hosted host %q prefixes: %w", normalizedHost, err)
		}
		record.ApprovedSelfHostedNetworkHosts = append(record.ApprovedSelfHostedNetworkHosts, &plugincontract.SelfHostedNetworkHostApproval{
			HostAndPort:       normalizedHost,
			AllowedIpPrefixes: append([]string(nil), approval.GetAllowedIpPrefixes()...),
		})
	}
	for _, rawPath := range req.ApprovedReadPaths {
		absPath, err := filepath.Abs(strings.TrimSpace(rawPath))
		if err != nil || strings.TrimSpace(rawPath) == "" {
			return nil, fmt.Errorf("approved read path %q is invalid", rawPath)
		}
		record.ApprovedReadPaths = append(record.ApprovedReadPaths, absPath)
	}
	for _, rawKey := range req.ApprovedReadSecretKeys {
		key := strings.TrimSpace(rawKey)
		if err := secrets.ValidatePluginSecretKey(key); err != nil {
			return nil, fmt.Errorf("approved read secret key: %w", err)
		}
		record.ApprovedReadSecretKeys = append(record.ApprovedReadSecretKeys, key)
	}
	for _, rawKey := range req.ApprovedWriteSecretKeys {
		key := strings.TrimSpace(rawKey)
		if err := secrets.ValidatePluginSecretKey(key); err != nil {
			return nil, fmt.Errorf("approved write secret key: %w", err)
		}
		record.ApprovedWriteSecretKeys = append(record.ApprovedWriteSecretKeys, key)
	}
	for _, rawOrigin := range req.ApprovedAuthOrigins {
		parsed, err := url.Parse(strings.TrimSpace(rawOrigin))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" {
			return nil, fmt.Errorf("approved auth origin %q must be https://host", rawOrigin)
		}
		record.ApprovedAuthOrigins = append(record.ApprovedAuthOrigins, "https://"+strings.ToLower(parsed.Host))
	}
	if override := strings.TrimSpace(req.ApprovedArchiveDirOverride); override != "" {
		absOverride, err := filepath.Abs(override)
		if err != nil {
			return nil, fmt.Errorf("approved archive directory %q: %w", override, err)
		}
		record.ApprovedArchiveDirectoryOverride = absOverride
	}
	return record, nil
}

func copyFileAtomically(sourcePath string, destinationPath string, perm os.FileMode) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(destinationPath), ".stage-bin-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destinationPath)
}

func writeBytesAtomically(destinationPath string, content []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(destinationPath), ".stage-trust-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destinationPath)
}
