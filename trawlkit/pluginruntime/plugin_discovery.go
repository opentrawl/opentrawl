package pluginruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/pluginproxy"
	"github.com/opentrawl/opentrawl/trawlkit/pluginsandbox"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	"github.com/opentrawl/opentrawl/trawlkit/secrets"
	"google.golang.org/protobuf/proto"
)

const (
	pluginDirsEnvironmentVariable = "OPENTRAWL_PLUGIN_DIRS"
	pluginExecutablePrefix        = "trawl-"
	stagedPluginBinaryFilename    = "staged.bin"
	pluginTrustRecordFilename     = "trust.pb"
	pluginManifestCacheDirectory  = ".manifest-cache"
)

// PluginDiscoveryOrigin identifies how a sandboxed plugin was loaded and which
// capability approval policy applies to it.
type PluginDiscoveryOrigin int

const (
	// PluginDiscoveryOriginStagedExternal represents a standalone external plugin
	// installed under <stateRoot>/plugins/<id>/staged.bin with a SHA-256-pinned
	// trust.pb record.
	PluginDiscoveryOriginStagedExternal PluginDiscoveryOrigin = iota
	// PluginDiscoveryOriginBundledMultiCall represents a first-party plugin
	// compiled into the host trawl binary and executed inside Seatbelt via
	// HiddenBundledPluginWorkerSubcommand.
	PluginDiscoveryOriginBundledMultiCall
	// PluginDiscoveryOriginEnvironmentDirectory represents a standalone external
	// plugin discovered via the OPENTRAWL_PLUGIN_DIRS environment variable.
	PluginDiscoveryOriginEnvironmentDirectory
)

// Label returns the short human-readable origin label shown by `trawl plugin list`.
func (origin PluginDiscoveryOrigin) Label() string {
	switch origin {
	case PluginDiscoveryOriginBundledMultiCall:
		return "bundled"
	case PluginDiscoveryOriginEnvironmentDirectory:
		return "external"
	default:
		return "staged"
	}
}

// TrustsDeclaredCapabilities reports whether the host grants capabilities
// declared by the plugin manifest without requiring a separate staged trust.pb
// approval record.
func (origin PluginDiscoveryOrigin) TrustsDeclaredCapabilities() bool {
	return origin == PluginDiscoveryOriginBundledMultiCall ||
		origin == PluginDiscoveryOriginEnvironmentDirectory
}

// BundledMultiCallPluginRegistration registers one first-party plugin compiled
// into the host trawl executable and run out-of-process inside Seatbelt.
type BundledMultiCallPluginRegistration struct {
	PluginIdentifier string
	Factory          func() trawlkit.Trawler
}

// DiscoveredExternalPlugin holds one loaded sandboxed plugin (either a bundled
// multi-call first-party plugin or a standalone external executable) together
// with its verified manifest, trust policy, and ready-to-use adapter.
type DiscoveredExternalPlugin struct {
	PluginIdentifier string
	ExecutablePath   string
	ExecutableArgs   []string
	ExecutableSHA256 string
	DiscoveryOrigin  PluginDiscoveryOrigin
	Manifest         *plugincontract.PluginManifestResponse
	TrustRecord      *plugincontract.PluginTrustRecord
	Trawler          trawlkit.Trawler
	DiscoveryError   error
}

// LoadBundledMultiCallPlugins builds in-process manifests and sandboxed
// multi-call worker adapters for first-party plugins compiled into the host
// trawl binary. Each plugin still runs out-of-process inside the exact same
// Seatbelt sandbox, capability policy, Keychain secret broker, and loopback
// HTTP allowlist proxy as standalone external plugins.
func LoadBundledMultiCallPlugins(
	stateRoot string,
	registrations []BundledMultiCallPluginRegistration,
	computeExecutableSHA256 bool,
) []DiscoveredExternalPlugin {
	if len(registrations) == 0 {
		return nil
	}
	resolvedStateRoot, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		resolvedStateRoot = ""
	}
	exePath, exeErr := os.Executable()
	canonicalExePath := ""
	if exeErr == nil {
		canonicalExePath = canonicalizeExecutablePath(exePath)
	}
	executableSHA256 := ""
	if computeExecutableSHA256 && canonicalExePath != "" {
		if digestHex, hashErr := computeFileSHA256Hex(canonicalExePath); hashErr == nil {
			executableSHA256 = digestHex
		}
	}

	discovered := make([]DiscoveredExternalPlugin, 0, len(registrations))
	for _, registration := range registrations {
		discovered = append(discovered, loadBundledMultiCallPlugin(
			resolvedStateRoot,
			canonicalExePath,
			executableSHA256,
			exeErr,
			registration,
		))
	}
	return discovered
}

func loadBundledMultiCallPlugin(
	resolvedStateRoot string,
	canonicalExePath string,
	executableSHA256 string,
	exeErr error,
	registration BundledMultiCallPluginRegistration,
) DiscoveredExternalPlugin {
	expectedID := strings.TrimSpace(registration.PluginIdentifier)
	if exeErr != nil || canonicalExePath == "" {
		return DiscoveredExternalPlugin{
			PluginIdentifier: expectedID,
			DiscoveryOrigin:  PluginDiscoveryOriginBundledMultiCall,
			DiscoveryError:   fmt.Errorf("resolve host trawl executable for bundled plugin %q: %w", expectedID, exeErr),
		}
	}
	if registration.Factory == nil {
		return DiscoveredExternalPlugin{
			PluginIdentifier: expectedID,
			ExecutablePath:   canonicalExePath,
			ExecutableSHA256: executableSHA256,
			DiscoveryOrigin:  PluginDiscoveryOriginBundledMultiCall,
			DiscoveryError:   fmt.Errorf("bundled plugin %q factory is nil", expectedID),
		}
	}
	manifestResp, err := trawlkit.BuildTrawlerPluginManifest(registration.Factory())
	if err != nil {
		return DiscoveredExternalPlugin{
			PluginIdentifier: expectedID,
			ExecutablePath:   canonicalExePath,
			ExecutableSHA256: executableSHA256,
			DiscoveryOrigin:  PluginDiscoveryOriginBundledMultiCall,
			DiscoveryError:   err,
		}
	}
	pluginID := trawlkit.RegisteredTrawlerIdentityText(manifestResp.GetRegisteredTrawlerManifest().GetRegisteredTrawler())
	if err := secrets.ValidatePluginIdentifier(pluginID); err != nil {
		return DiscoveredExternalPlugin{
			PluginIdentifier: pluginID,
			ExecutablePath:   canonicalExePath,
			ExecutableSHA256: executableSHA256,
			DiscoveryOrigin:  PluginDiscoveryOriginBundledMultiCall,
			Manifest:         manifestResp,
			DiscoveryError:   fmt.Errorf("invalid bundled plugin identity: %w", err),
		}
	}
	if expectedID != "" && expectedID != pluginID {
		return DiscoveredExternalPlugin{
			PluginIdentifier: pluginID,
			ExecutablePath:   canonicalExePath,
			ExecutableSHA256: executableSHA256,
			DiscoveryOrigin:  PluginDiscoveryOriginBundledMultiCall,
			Manifest:         manifestResp,
			DiscoveryError:   fmt.Errorf("bundled plugin registration %q does not match manifest identity %q", expectedID, pluginID),
		}
	}
	trustRecord := loadOptionalPluginTrustRecord(resolvedStateRoot, pluginID)
	workerArgs := []string{trawlkit.HiddenBundledPluginWorkerSubcommand, pluginID}
	adapter := NewExternalPluginTrawler(ExternalPluginTrawlerConfig{
		PluginIdentifier: pluginID,
		ExecutablePath:   canonicalExePath,
		ExecutableArgs:   workerArgs,
		ExecutableSHA256: executableSHA256,
		DiscoveryOrigin:  PluginDiscoveryOriginBundledMultiCall,
		Manifest:         manifestResp,
		TrustRecord:      trustRecord,
	})
	return DiscoveredExternalPlugin{
		PluginIdentifier: pluginID,
		ExecutablePath:   canonicalExePath,
		ExecutableArgs:   workerArgs,
		ExecutableSHA256: executableSHA256,
		DiscoveryOrigin:  PluginDiscoveryOriginBundledMultiCall,
		Manifest:         manifestResp,
		TrustRecord:      trustRecord,
		Trawler:          adapter,
	}
}

// DiscoverExternalPlugins locates standalone external plugin executables from
// OPENTRAWL_PLUGIN_DIRS and user-installed staged plugins under stateRoot,
// probes and caches manifests under a zero-privilege Seatbelt sandbox, and
// rejects identifiers that collide with reservedBuiltInIDs.
func DiscoverExternalPlugins(
	ctx context.Context,
	stateRoot string,
	reservedBuiltInIDs map[string]struct{},
) []DiscoveredExternalPlugin {
	resolvedStateRoot, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return nil
	}

	candidates := collectExternalPluginCandidates(resolvedStateRoot)
	if len(candidates) == 0 {
		return nil
	}

	seenPluginIDs := make(map[string]struct{}, len(candidates))
	discovered := make([]DiscoveredExternalPlugin, 0, len(candidates))
	for _, candidate := range candidates {
		entry := loadExternalPluginCandidate(ctx, resolvedStateRoot, candidate, reservedBuiltInIDs, seenPluginIDs)
		if entry == nil {
			continue
		}
		if entry.PluginIdentifier != "" {
			seenPluginIDs[entry.PluginIdentifier] = struct{}{}
		}
		discovered = append(discovered, *entry)
	}
	return discovered
}

type externalPluginCandidate struct {
	expectedPluginID string
	executablePath   string
	discoveryOrigin  PluginDiscoveryOrigin
	trustRecordPath  string
}

func collectExternalPluginCandidates(stateRoot string) []externalPluginCandidate {
	var candidates []externalPluginCandidate
	seenExecPaths := make(map[string]struct{})
	addCandidate := func(candidate externalPluginCandidate) {
		canonicalPath := canonicalizeExecutablePath(candidate.executablePath)
		if canonicalPath == "" {
			return
		}
		if _, dup := seenExecPaths[canonicalPath]; dup {
			return
		}
		seenExecPaths[canonicalPath] = struct{}{}
		candidate.executablePath = canonicalPath
		candidates = append(candidates, candidate)
	}

	for _, dir := range environmentPluginSearchDirectories() {
		for _, execPath := range scanExternalPluginDirectory(dir) {
			addCandidate(externalPluginCandidate{
				executablePath:  execPath,
				discoveryOrigin: PluginDiscoveryOriginEnvironmentDirectory,
			})
		}
	}

	pluginsDir := filepath.Join(stateRoot, "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			pluginID := entry.Name()
			stagedPath := filepath.Join(pluginsDir, pluginID, stagedPluginBinaryFilename)
			if !isExecutableRegularFile(stagedPath) {
				continue
			}
			addCandidate(externalPluginCandidate{
				expectedPluginID: pluginID,
				executablePath:   stagedPath,
				discoveryOrigin:  PluginDiscoveryOriginStagedExternal,
				trustRecordPath:  filepath.Join(pluginsDir, pluginID, pluginTrustRecordFilename),
			})
		}
	}
	return candidates
}

func environmentPluginSearchDirectories() []string {
	envDirs := strings.TrimSpace(os.Getenv(pluginDirsEnvironmentVariable))
	if envDirs == "" {
		return nil
	}
	var dirs []string
	seen := make(map[string]struct{})
	for _, raw := range filepath.SplitList(envDirs) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			continue
		}
		if _, dup := seen[abs]; dup {
			continue
		}
		seen[abs] = struct{}{}
		dirs = append(dirs, abs)
	}
	return dirs
}

func scanExternalPluginDirectory(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, pluginExecutablePrefix) || strings.HasSuffix(name, ".dSYM") {
			continue
		}
		// Never treat internal helper binaries or test runners as trawler plugins.
		if name == "trawl-clib" || name == "trawl-embed" || strings.HasSuffix(name, ".test") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if isExecutableRegularFile(fullPath) {
			paths = append(paths, fullPath)
		}
	}
	sort.Strings(paths)
	return paths
}

func isExecutableRegularFile(path string) bool {
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() {
		return false
	}
	return stat.Mode().Perm()&0o111 != 0
}

func canonicalizeExecutablePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		return resolved
	}
	return abs
}

func loadExternalPluginCandidate(
	ctx context.Context,
	stateRoot string,
	candidate externalPluginCandidate,
	reservedBuiltInIDs map[string]struct{},
	seenPluginIDs map[string]struct{},
) *DiscoveredExternalPlugin {
	digestHex, err := computeFileSHA256Hex(candidate.executablePath)
	if err != nil {
		return nil
	}

	var trustRecord *plugincontract.PluginTrustRecord
	if candidate.discoveryOrigin == PluginDiscoveryOriginStagedExternal {
		loadedTrust, trustErr := loadAndVerifyPluginTrustRecord(candidate.trustRecordPath, candidate.expectedPluginID, digestHex)
		if trustErr != nil {
			return &DiscoveredExternalPlugin{
				PluginIdentifier: candidate.expectedPluginID,
				ExecutablePath:   candidate.executablePath,
				ExecutableSHA256: digestHex,
				DiscoveryOrigin:  candidate.discoveryOrigin,
				DiscoveryError:   trustErr,
			}
		}
		trustRecord = loadedTrust
	} else {
		trustRecord = loadOptionalPluginTrustRecord(stateRoot, candidate.expectedPluginID)
	}

	manifestResp, err := loadOrProbePluginManifest(ctx, stateRoot, candidate.executablePath, digestHex)
	if err != nil {
		return &DiscoveredExternalPlugin{
			PluginIdentifier: candidate.expectedPluginID,
			ExecutablePath:   candidate.executablePath,
			ExecutableSHA256: digestHex,
			DiscoveryOrigin:  candidate.discoveryOrigin,
			DiscoveryError:   err,
		}
	}

	pluginID := trawlkit.RegisteredTrawlerIdentityText(manifestResp.GetRegisteredTrawlerManifest().GetRegisteredTrawler())
	if err := validateDiscoveredPluginIdentity(pluginID, candidate, reservedBuiltInIDs, seenPluginIDs); err != nil {
		return &DiscoveredExternalPlugin{
			PluginIdentifier: pluginID,
			ExecutablePath:   candidate.executablePath,
			ExecutableSHA256: digestHex,
			DiscoveryOrigin:  candidate.discoveryOrigin,
			Manifest:         manifestResp,
			DiscoveryError:   err,
		}
	}

	if candidate.discoveryOrigin != PluginDiscoveryOriginStagedExternal && trustRecord == nil {
		trustRecord = loadOptionalPluginTrustRecord(stateRoot, pluginID)
	}

	adapter := NewExternalPluginTrawler(ExternalPluginTrawlerConfig{
		PluginIdentifier: pluginID,
		ExecutablePath:   candidate.executablePath,
		ExecutableSHA256: digestHex,
		DiscoveryOrigin:  candidate.discoveryOrigin,
		Manifest:         manifestResp,
		TrustRecord:      trustRecord,
	})
	return &DiscoveredExternalPlugin{
		PluginIdentifier: pluginID,
		ExecutablePath:   candidate.executablePath,
		ExecutableSHA256: digestHex,
		DiscoveryOrigin:  candidate.discoveryOrigin,
		Manifest:         manifestResp,
		TrustRecord:      trustRecord,
		Trawler:          adapter,
	}
}

func validateDiscoveredPluginIdentity(
	pluginID string,
	candidate externalPluginCandidate,
	reservedBuiltInIDs map[string]struct{},
	seenPluginIDs map[string]struct{},
) error {
	if err := secrets.ValidatePluginIdentifier(pluginID); err != nil {
		return fmt.Errorf("invalid plugin identity: %w", err)
	}
	if candidate.expectedPluginID != "" && candidate.expectedPluginID != pluginID {
		return fmt.Errorf("staged plugin directory %q does not match manifest identity %q", candidate.expectedPluginID, pluginID)
	}
	if _, collides := reservedBuiltInIDs[pluginID]; collides {
		return fmt.Errorf("external plugin identity %q collides with a built-in trawler", pluginID)
	}
	if _, duplicate := seenPluginIDs[pluginID]; duplicate {
		return fmt.Errorf("duplicate external plugin identity %q", pluginID)
	}
	return nil
}

func loadAndVerifyPluginTrustRecord(trustPath string, expectedPluginID string, actualSHA256Hex string) (*plugincontract.PluginTrustRecord, error) {
	encoded, err := os.ReadFile(trustPath)
	if err != nil {
		return nil, fmt.Errorf("read plugin trust record %q: %w", trustPath, err)
	}
	record := &plugincontract.PluginTrustRecord{}
	if err := proto.Unmarshal(encoded, record); err != nil {
		return nil, fmt.Errorf("decode plugin trust record %q: %w", trustPath, err)
	}
	if strings.TrimSpace(record.GetPluginIdentifier()) != expectedPluginID {
		return nil, fmt.Errorf("plugin trust record identity %q does not match %q", record.GetPluginIdentifier(), expectedPluginID)
	}
	if !strings.EqualFold(strings.TrimSpace(record.GetApprovedBinarySha256Hex()), actualSHA256Hex) {
		return nil, fmt.Errorf("plugin %q binary hash does not match approved trust record", expectedPluginID)
	}
	return record, nil
}

func loadOptionalPluginTrustRecord(stateRoot string, pluginID string) *plugincontract.PluginTrustRecord {
	if strings.TrimSpace(pluginID) == "" {
		return nil
	}
	trustPath := filepath.Join(stateRoot, "plugins", pluginID, pluginTrustRecordFilename)
	encoded, err := os.ReadFile(trustPath)
	if err != nil {
		return nil
	}
	record := &plugincontract.PluginTrustRecord{}
	if err := proto.Unmarshal(encoded, record); err != nil {
		return nil
	}
	return record
}

func loadOrProbePluginManifest(
	ctx context.Context,
	stateRoot string,
	executablePath string,
	digestHex string,
) (*plugincontract.PluginManifestResponse, error) {
	cachePath := filepath.Join(stateRoot, "plugins", pluginManifestCacheDirectory, digestHex+".pb")
	if encoded, err := os.ReadFile(cachePath); err == nil {
		cached := &plugincontract.CachedPluginManifestRecord{}
		if unmarshalErr := proto.Unmarshal(encoded, cached); unmarshalErr == nil &&
			cached.GetExecutableSha256Hex() == digestHex &&
			cached.GetPluginManifest().GetRegisteredTrawlerManifest() != nil {
			return cached.GetPluginManifest(), nil
		}
	}

	outcome, err := invokeSandboxedPlugin(ctx, pluginInvocationExecutionOptions{
		ExecutablePath: executablePath,
		SandboxPolicy: pluginsandbox.PluginSandboxPolicy{
			ExecutablePath: executablePath,
		},
		Request: &plugincontract.PluginInvocationRequest{
			Operation: &plugincontract.PluginInvocationRequest_Manifest{
				Manifest: &plugincontract.PluginManifestOperationRequest{},
			},
		},
		HardDeadline: pluginManifestProbeTimeout,
		IdleWatchdog: pluginManifestProbeTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("probe plugin manifest %q: %w", filepath.Base(executablePath), err)
	}
	manifestResp := outcome.Result.GetManifest()
	if manifestResp == nil || manifestResp.GetRegisteredTrawlerManifest() == nil {
		return nil, fmt.Errorf("plugin %q returned empty manifest response", filepath.Base(executablePath))
	}

	saveManifestCache(cachePath, digestHex, manifestResp)
	return manifestResp, nil
}

func saveManifestCache(cachePath string, digestHex string, manifest *plugincontract.PluginManifestResponse) {
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return
	}
	encoded, err := proto.Marshal(&plugincontract.CachedPluginManifestRecord{
		ExecutableSha256Hex: digestHex,
		PluginManifest:      manifest,
	})
	if err != nil {
		return
	}
	tmpPath := cachePath + ".tmp"
	if err := os.WriteFile(tmpPath, encoded, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmpPath, cachePath)
}

func computeFileSHA256Hex(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// parseTrustSelfHostedApprovals converts protobuf self-hosted host approvals
// into validated proxy exemptions.
func parseTrustSelfHostedApprovals(approvals []*plugincontract.SelfHostedNetworkHostApproval) ([]pluginproxy.SelfHostedNetworkHostExemption, error) {
	exemptions := make([]pluginproxy.SelfHostedNetworkHostExemption, 0, len(approvals))
	for _, approval := range approvals {
		if approval == nil {
			continue
		}
		prefixes, err := pluginproxy.ParseIPPrefixes(approval.GetAllowedIpPrefixes())
		if err != nil {
			return nil, fmt.Errorf("self-hosted approval for %q: %w", approval.GetHostAndPort(), err)
		}
		exemptions = append(exemptions, pluginproxy.SelfHostedNetworkHostExemption{
			HostAndPort:       approval.GetHostAndPort(),
			AllowedIPPrefixes: prefixes,
		})
	}
	return exemptions, nil
}

// extractBundledConfigReadPaths reads host-owned read-only config.toml for a
// bundled first-party plugin and returns existing file and directory paths
// referenced by the plugin's declared_config_read_path_fields (e.g.
// "enable_banking.private_key_file", "source").
func extractBundledConfigReadPaths(configPath string, fieldPaths []string) (files []string, dirs []string) {
	if strings.TrimSpace(configPath) == "" || len(fieldPaths) == 0 {
		return nil, nil
	}
	encoded, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil
	}
	wanted := make(map[string]struct{}, len(fieldPaths))
	for _, field := range fieldPaths {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			wanted[trimmed] = struct{}{}
		}
	}
	return resolveExistingBundledReadPaths(scanTOMLStringValuesForDottedFields(string(encoded), wanted))
}

func resolveExistingBundledReadPaths(rawPaths []string) (files []string, dirs []string) {
	seen := make(map[string]struct{})
	for _, rawPath := range rawPaths {
		expanded := expandBundledHomeRelativePath(rawPath)
		if expanded == "" || !filepath.IsAbs(expanded) {
			continue
		}
		stat, statErr := os.Stat(expanded)
		if statErr != nil {
			continue
		}
		if _, dup := seen[expanded]; dup {
			continue
		}
		seen[expanded] = struct{}{}
		if stat.IsDir() {
			dirs = append(dirs, expanded)
		} else if stat.Mode().IsRegular() {
			files = append(files, expanded)
		}
	}
	return files, dirs
}

func expandBundledHomeRelativePath(rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return ""
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		if trimmed == "~" {
			return filepath.Clean(home)
		}
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	}
	return filepath.Clean(trimmed)
}

func scanTOMLStringValuesForDottedFields(content string, wantedFields map[string]struct{}) []string {
	var values []string
	currentSection := ""
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			currentSection = strings.TrimSpace(line[2 : len(line)-2])
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		keyPart, valuePart, hasEq := strings.Cut(line, "=")
		if !hasEq {
			continue
		}
		key := strings.TrimSpace(keyPart)
		fullKey := key
		if currentSection != "" {
			fullKey = currentSection + "." + key
		}
		if _, match := wantedFields[fullKey]; !match {
			continue
		}
		if parsed, ok := unquoteTOMLStringLiteral(strings.TrimSpace(valuePart)); ok {
			values = append(values, parsed)
		}
	}
	return values
}

func unquoteTOMLStringLiteral(raw string) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	if raw[0] == '\'' {
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			return "", false
		}
		return raw[1 : 1+end], true
	}
	if raw[0] != '"' {
		return "", false
	}
	var b strings.Builder
	escaped := false
	for i := 1; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return b.String(), true
		}
		b.WriteByte(ch)
	}
	return "", false
}
