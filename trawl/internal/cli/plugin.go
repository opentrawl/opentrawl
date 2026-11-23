package cli

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type PluginCmd struct {
	List    PluginListCmd    `cmd:"" default:"withargs" help:"List bundled and external plugins"`
	Install PluginInstallCmd `cmd:"" help:"Stage a standalone plugin executable and pin its hash and capabilities"`
	Remove  PluginRemoveCmd  `cmd:"" help:"Remove a staged standalone plugin; keep its archive"`
}

type PluginListCmd struct{}

type PluginInstallCmd struct {
	Path             string   `arg:"" name:"path" help:"Path to standalone plugin executable"`
	AllowHost        []string `name:"allow-host" placeholder:"HOST:PORT" help:"Approve an outbound public HTTPS host and port"`
	AllowSelfHosted  []string `name:"allow-self-hosted" placeholder:"HOST:PORT=CIDR" help:"Approve an outbound self-hosted HTTPS host on explicit IP ranges"`
	AllowReadPath    []string `name:"allow-read-path" placeholder:"PATH" help:"Approve a read-only file or directory path"`
	AllowReadSecret  []string `name:"allow-read-secret" placeholder:"KEY" help:"Approve a readable Keychain secret key"`
	AllowWriteSecret []string `name:"allow-write-secret" placeholder:"KEY" help:"Approve a writable Keychain secret key"`
	AllowAuthOrigin  []string `name:"allow-auth-origin" placeholder:"ORIGIN" help:"Approve an https:// authentication origin"`
	ArchiveDir       string   `name:"archive-dir" placeholder:"DIR" help:"Override the plugin archive directory"`
}

type PluginRemoveCmd struct {
	Plugin string `arg:"" name:"plugin" help:"Staged plugin identifier to remove"`
}

func (c *PluginListCmd) Run(r *Runtime) error {
	bundled := pluginruntime.LoadBundledMultiCallPlugins(r.stateRoot, bundledMultiCallPlugins, true)
	external := pluginruntime.DiscoverExternalPlugins(r.ctx, r.stateRoot, reservedFirstPartyTrawlerIdentitySet())
	rows := make([]render.ExternalPluginRowPresentation, 0, len(bundled)+len(external))
	for _, plugin := range bundled {
		rows = append(rows, externalPluginRowPresentation(plugin))
	}
	for _, plugin := range external {
		rows = append(rows, externalPluginRowPresentation(plugin))
	}
	return r.output.WriteExternalPluginList(render.ExternalPluginListPresentation{Plugins: rows})
}

func (c *PluginInstallCmd) Run(r *Runtime) error {
	selfHostedApprovals := make([]*plugincontract.SelfHostedNetworkHostApproval, 0, len(c.AllowSelfHosted))
	for _, spec := range c.AllowSelfHosted {
		parsed, err := pluginruntime.ParseSelfHostedHostApprovalSpec(spec)
		if err != nil {
			return render.ExternalPluginCommandUsageFailed(err.Error())
		}
		selfHostedApprovals = append(selfHostedApprovals, parsed)
	}
	outcome, err := pluginruntime.StageExternalPluginExecutable(
		r.ctx,
		r.stateRoot,
		pluginruntime.StageExternalPluginRequest{
			SourceExecutablePath:       c.Path,
			ApprovedReadPaths:          c.AllowReadPath,
			ApprovedNetworkHosts:       c.AllowHost,
			ApprovedSelfHostedHosts:    selfHostedApprovals,
			ApprovedReadSecretKeys:     c.AllowReadSecret,
			ApprovedWriteSecretKeys:    c.AllowWriteSecret,
			ApprovedAuthOrigins:        c.AllowAuthOrigin,
			ApprovedArchiveDirOverride: c.ArchiveDir,
		},
		reservedFirstPartyTrawlerIdentitySet(),
	)
	if err != nil {
		return render.ExternalPluginOperationFailed(err.Error())
	}
	return r.output.WriteExternalPluginInstalled(render.ExternalPluginInstalledPresentation{
		PluginIdentifier:               outcome.PluginIdentifier,
		DisplayName:                    outcome.DisplayName,
		StagedBinaryPath:               outcome.StagedBinaryPath,
		ExecutableSHA256:               outcome.ExecutableSHA256,
		ApprovedCapabilitiesSummary:    formatTrustRecordCapabilities(outcome.TrustRecord),
		UnapprovedDeclaredCapabilities: findUnapprovedDeclaredCapabilities(outcome.Manifest.GetDeclaredCapabilities(), outcome.TrustRecord),
	})
}

func (c *PluginRemoveCmd) Run(r *Runtime) error {
	removed, err := pluginruntime.RemoveStagedExternalPlugin(r.stateRoot, c.Plugin)
	if err != nil {
		return render.ExternalPluginOperationFailed(err.Error())
	}
	return r.output.WriteExternalPluginRemoved(c.Plugin, removed)
}

func reservedFirstPartyTrawlerIdentitySet() map[string]struct{} {
	identities := make(map[string]struct{}, len(trawlerFactories)+len(bundledMultiCallPlugins))
	for _, registration := range trawlerFactories {
		trawler := registration.factory()
		declaration := trawler.RegisteredTrawlerDeclaration()
		if id := trawlkit.RegisteredTrawlerIdentityText(declaration.RegisteredTrawler); id != "" {
			identities[id] = struct{}{}
		}
	}
	for _, registration := range bundledMultiCallPlugins {
		if id := strings.TrimSpace(registration.PluginIdentifier); id != "" {
			identities[id] = struct{}{}
		}
	}
	return identities
}

func externalPluginRowPresentation(plugin pluginruntime.DiscoveredExternalPlugin) render.ExternalPluginRowPresentation {
	status := "ok"
	errorSentence := ""
	if plugin.DiscoveryError != nil {
		status = "error"
		errorSentence = plugin.DiscoveryError.Error()
	}
	shortHash := plugin.ExecutableSHA256
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	displayName := strings.TrimSpace(plugin.Manifest.GetRegisteredTrawlerManifest().GetRegisteredTrawlerDisplayName())
	return render.ExternalPluginRowPresentation{
		PluginIdentifier:            plugin.PluginIdentifier,
		DisplayName:                 displayName,
		OriginLabel:                 plugin.DiscoveryOrigin.Label(),
		StatusLabel:                 status,
		ExecutableSHA256Short:       shortHash,
		ApprovedCapabilitiesSummary: formatDiscoveredPluginCapabilities(plugin),
		ErrorSentence:               errorSentence,
	}
}

func formatDiscoveredPluginCapabilities(plugin pluginruntime.DiscoveredExternalPlugin) string {
	var parts []string
	if plugin.DiscoveryOrigin.TrustsDeclaredCapabilities() {
		declared := plugin.Manifest.GetDeclaredCapabilities()
		for _, host := range declared.GetDeclaredNetworkHosts() {
			parts = append(parts, "host:"+host)
		}
		for _, path := range declared.GetDeclaredReadPaths() {
			parts = append(parts, "read:"+path)
		}
		for _, field := range declared.GetDeclaredConfigReadPathFields() {
			parts = append(parts, "config-read:"+field)
		}
		for _, key := range declared.GetDeclaredReadSecretKeys() {
			parts = append(parts, "secret-read:"+key)
		}
		for _, key := range declared.GetDeclaredWriteSecretKeys() {
			parts = append(parts, "secret-write:"+key)
		}
	}
	if trustSummary := formatTrustRecordCapabilities(plugin.TrustRecord); trustSummary != "" {
		parts = append(parts, trustSummary)
	}
	return strings.Join(parts, ", ")
}

func formatTrustRecordCapabilities(record *plugincontract.PluginTrustRecord) string {
	if record == nil {
		return ""
	}
	var parts []string
	for _, host := range record.GetApprovedNetworkHosts() {
		parts = append(parts, "host:"+host)
	}
	for _, selfHosted := range record.GetApprovedSelfHostedNetworkHosts() {
		parts = append(parts, fmt.Sprintf("self-hosted:%s=%s", selfHosted.GetHostAndPort(), strings.Join(selfHosted.GetAllowedIpPrefixes(), ",")))
	}
	for _, path := range record.GetApprovedReadPaths() {
		parts = append(parts, "read:"+path)
	}
	for _, key := range record.GetApprovedReadSecretKeys() {
		parts = append(parts, "secret-read:"+key)
	}
	for _, key := range record.GetApprovedWriteSecretKeys() {
		parts = append(parts, "secret-write:"+key)
	}
	for _, origin := range record.GetApprovedAuthOrigins() {
		parts = append(parts, "auth:"+origin)
	}
	if override := strings.TrimSpace(record.GetApprovedArchiveDirectoryOverride()); override != "" {
		parts = append(parts, "archive-dir:"+override)
	}
	return strings.Join(parts, ", ")
}

func findUnapprovedDeclaredCapabilities(
	declared *plugincontract.PluginDeclaredCapabilities,
	trust *plugincontract.PluginTrustRecord,
) []string {
	if declared == nil {
		return nil
	}
	approvedHosts := stringSet(trust.GetApprovedNetworkHosts())
	for _, selfHosted := range trust.GetApprovedSelfHostedNetworkHosts() {
		approvedHosts[selfHosted.GetHostAndPort()] = struct{}{}
	}
	approvedReadPaths := stringSet(trust.GetApprovedReadPaths())
	approvedReadSecrets := stringSet(trust.GetApprovedReadSecretKeys())
	approvedWriteSecrets := stringSet(trust.GetApprovedWriteSecretKeys())
	approvedOrigins := stringSet(trust.GetApprovedAuthOrigins())

	var unapproved []string
	for _, host := range declared.GetDeclaredNetworkHosts() {
		if _, ok := approvedHosts[host]; !ok {
			unapproved = append(unapproved, "--allow-host "+host)
		}
	}
	for _, path := range declared.GetDeclaredReadPaths() {
		if _, ok := approvedReadPaths[path]; !ok {
			unapproved = append(unapproved, "--allow-read-path "+path)
		}
	}
	for _, key := range declared.GetDeclaredReadSecretKeys() {
		if _, ok := approvedReadSecrets[key]; !ok {
			unapproved = append(unapproved, "--allow-read-secret "+key)
		}
	}
	for _, key := range declared.GetDeclaredWriteSecretKeys() {
		if _, ok := approvedWriteSecrets[key]; !ok {
			unapproved = append(unapproved, "--allow-write-secret "+key)
		}
	}
	for _, origin := range declared.GetDeclaredAuthOrigins() {
		if _, ok := approvedOrigins[origin]; !ok {
			unapproved = append(unapproved, "--allow-auth-origin "+origin)
		}
	}
	return unapproved
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = struct{}{}
	}
	return result
}
