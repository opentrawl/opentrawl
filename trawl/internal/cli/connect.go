package cli

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

//go:embed skill/SKILL.md
var canonicalOpenTrawlSkillMarkdown string

// ConnectCmd auto-detects installed local agents, shows the exact MCP (and
// optional SKILL.md) configuration diff, and writes the configuration with
// per-agent source-scoped bearer tokens when --write is set.
type ConnectCmd struct {
	Agent     string `arg:"" optional:"" name:"agent" help:"Agent to connect (claude-code, claude-desktop, codex, executor, openclaw, hermes, cursor, t3, or all); omit to auto-detect installed agents"`
	Sources   string `name:"sources" default:"all" placeholder:"TRAWLERS" help:"Sources each agent may read, separated by commas, or all"`
	Transport string `name:"transport" default:"stdio" enum:"stdio,http" help:"stdio runs trawl mcp locally (default); http connects to trawl mcp --http"`
	Port      int    `name:"port" default:"7391" help:"Port trawl mcp --http listens on"`
	Write     bool   `name:"write" help:"Write the shown configuration changes and mint per-agent scoped tokens"`
	Skill     bool   `name:"skill" help:"Include the canonical OpenTrawl SKILL.md diff and write it with --write"`
}

const (
	connectPreviewTokenPlaceholder = "<new-scoped-token>"
	macOSAppBundledTrawlPath       = "/Applications/OpenTrawl.app/Contents/Helpers/trawl"
)

type connectableAgentConfigFormat int

const (
	connectableAgentConfigFormatJSON connectableAgentConfigFormat = iota + 1
	connectableAgentConfigFormatCodexTOML
	connectableAgentConfigFormatHermesYAML
	connectableAgentConfigFormatCompositeT3
)

type connectableAgent struct {
	agentCommandName string
	agentDisplayName string
	configFormat     connectableAgentConfigFormat
}

var connectableAgents = []connectableAgent{
	{agentCommandName: "claude-code", agentDisplayName: "Claude Code", configFormat: connectableAgentConfigFormatJSON},
	{agentCommandName: "claude-desktop", agentDisplayName: "Claude Desktop", configFormat: connectableAgentConfigFormatJSON},
	{agentCommandName: "codex", agentDisplayName: "Codex", configFormat: connectableAgentConfigFormatCodexTOML},
	{agentCommandName: "executor", agentDisplayName: "executor.sh", configFormat: connectableAgentConfigFormatJSON},
	{agentCommandName: "openclaw", agentDisplayName: "OpenClaw", configFormat: connectableAgentConfigFormatJSON},
	{agentCommandName: "hermes", agentDisplayName: "Hermes Agent", configFormat: connectableAgentConfigFormatHermesYAML},
	{agentCommandName: "cursor", agentDisplayName: "Cursor", configFormat: connectableAgentConfigFormatJSON},
	{agentCommandName: "t3", agentDisplayName: "T3 Code", configFormat: connectableAgentConfigFormatCompositeT3},
}

type agentMCPConnection struct {
	transport       string
	trawlExecutable string
	serverURL       string
	bearerToken     string
}

type opentrawlStdioMCPServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type opentrawlHTTPMCPServerEntry struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func (c *ConnectCmd) Run(r *Runtime) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot locate home directory: " + err.Error())
	}
	if c.Port < 1 || c.Port > 65535 {
		return render.AgentAccessCommandIsWrong("--port must be between 1 and 65535")
	}

	requested := strings.ToLower(strings.TrimSpace(c.Agent))
	detectedByName := detectInstalledLocalAgents(home)
	agents, includeSkillByTarget, err := resolveConnectTargetAgents(requested, detectedByName)
	if err != nil {
		return err
	}
	includeSkill := c.Skill || includeSkillByTarget

	trawlExecutable := resolveTrawlExecutableForMCPConfig()
	serverURL, hostSource := resolveConnectHTTPServerURL(r.ctx, c.Port)
	skillPath := canonicalOpenTrawlSkillPath(home)

	facts := []render.FieldValueRow{
		{FieldLabel: "transport", FieldValue: c.Transport},
		{FieldLabel: "sources", FieldValue: c.Sources},
		{FieldLabel: "mode", FieldValue: connectModeLabel(c.Write)},
	}
	if c.Transport == "stdio" {
		facts = append(facts, render.FieldValueRow{FieldLabel: "command", FieldValue: trawlExecutable + " mcp"})
	} else {
		listenAddress := "127.0.0.1:" + strconv.Itoa(c.Port)
		if hostSource == "Tailscale MagicDNS" {
			listenAddress = "0.0.0.0:" + strconv.Itoa(c.Port)
		}
		facts = append(facts,
			render.FieldValueRow{FieldLabel: "server", FieldValue: serverURL + " (" + hostSource + ")"},
			render.FieldValueRow{FieldLabel: "start it", FieldValue: "trawl mcp --http --listen " + listenAddress},
		)
	}
	facts = append(facts, render.FieldValueRow{FieldLabel: "skill", FieldValue: canonicalSkillStatusFact(skillPath)})

	if len(agents) == 0 && !includeSkill {
		names := connectableAgentNames()
		return r.output.WriteAgentConnectionSetup(render.AgentConnectionSetupPresentation{
			AgentConnectionSummarySentence: "No supported local agents were detected. Name an agent to preview its configuration (" + strings.Join(names, ", ") + ", or all).",
			AgentConnectionFacts:           facts,
			NextTrawlCommands:              [][]string{{"connect", "claude-code"}, {"connect", "--skill"}},
		})
	}

	summary := "Previewing exact OpenTrawl MCP configuration diffs. No files or tokens were changed."
	if c.Write {
		summary = "Configured OpenTrawl MCP access with per-agent source-scoped tokens."
	} else if requested == "" {
		summary = "Detected installed local agents. Previewing exact configuration diffs; no files or tokens were changed."
	}

	setup := render.AgentConnectionSetupPresentation{
		AgentConnectionSummarySentence: summary,
		AgentConnectionFacts:           facts,
	}

	seenConfigPaths := map[string]bool{}
	for _, agent := range agents {
		if agent.configFormat == connectableAgentConfigFormatCompositeT3 {
			setup.AgentConnectionConfigurations = append(setup.AgentConnectionConfigurations, render.AgentConnectionConfigurationPresentation{
				AgentDisplayName:                   agent.agentDisplayName,
				AgentDetectedLocally:               detectedByName[agent.agentCommandName],
				AgentConnectionStatusSentence:      connectAgentDetectionLabel(detectedByName[agent.agentCommandName]),
				AgentConnectionInstructionSentence: "T3 Code delegates MCP servers to Claude Code (~/.claude.json) and Codex (~/.codex/config.toml), and reads canonical skills from ~/.agents/skills/opentrawl/SKILL.md.",
			})
			continue
		}
		configPath := agentConfigFilePath(agent.agentCommandName, home)
		if seenConfigPaths[configPath] {
			continue
		}
		seenConfigPaths[configPath] = true

		configuration, configErr := buildOrApplyAgentMCPConfiguration(
			r.stateRoot,
			agent,
			detectedByName[agent.agentCommandName],
			configPath,
			c.Sources,
			agentMCPConnection{
				transport:       c.Transport,
				trawlExecutable: trawlExecutable,
				serverURL:       serverURL,
				bearerToken:     connectPreviewTokenPlaceholder,
			},
			c.Write,
		)
		if configErr != nil {
			return render.AgentAccessOperationFailed(agent.agentDisplayName + ": " + configErr.Error())
		}
		setup.AgentConnectionConfigurations = append(setup.AgentConnectionConfigurations, configuration)
	}

	if includeSkill {
		skillConfig, skillErr := buildOrApplyCanonicalSkill(skillPath, c.Write)
		if skillErr != nil {
			return render.AgentAccessOperationFailed("Canonical SKILL.md: " + skillErr.Error())
		}
		setup.AgentConnectionConfigurations = append(setup.AgentConnectionConfigurations, skillConfig)
		for index := range setup.AgentConnectionFacts {
			if setup.AgentConnectionFacts[index].FieldLabel == "skill" {
				setup.AgentConnectionFacts[index].FieldValue = canonicalSkillStatusFact(skillPath)
			}
		}
	}

	if !c.Write {
		nextArgs := []string{"connect"}
		if requested != "" {
			nextArgs = append(nextArgs, requested)
		}
		if c.Sources != "" && c.Sources != agentAccessAllSourcesKeyword {
			nextArgs = append(nextArgs, "--sources", c.Sources)
		}
		if c.Transport != "stdio" {
			nextArgs = append(nextArgs, "--transport", c.Transport)
		}
		if c.Skill {
			nextArgs = append(nextArgs, "--skill")
		}
		nextArgs = append(nextArgs, "--write")
		setup.NextTrawlCommands = [][]string{nextArgs}
	} else {
		setup.NextTrawlCommands = [][]string{{"token", "list"}}
	}

	return r.output.WriteAgentConnectionSetup(setup)
}

func resolveConnectTargetAgents(requested string, detectedByName map[string]bool) ([]connectableAgent, bool, error) {
	if requested == "" {
		var detected []connectableAgent
		for _, agent := range connectableAgents {
			if detectedByName[agent.agentCommandName] {
				detected = append(detected, agent)
			}
		}
		return detected, false, nil
	}
	if requested == "all" {
		return append([]connectableAgent(nil), connectableAgents...), false, nil
	}
	if requested == "t3" {
		var targets []connectableAgent
		for _, name := range []string{"t3", "claude-code", "codex"} {
			if agent, ok := findConnectableAgent(name); ok {
				targets = append(targets, agent)
			}
		}
		return targets, true, nil
	}
	agent, ok := findConnectableAgent(requested)
	if !ok {
		return nil, false, render.AgentAccessCommandIsWrong("Unknown agent " + requested + "; use one of " + strings.Join(connectableAgentNames(), ", ") + ", or all")
	}
	return []connectableAgent{agent}, false, nil
}

func findConnectableAgent(commandName string) (connectableAgent, bool) {
	for _, agent := range connectableAgents {
		if agent.agentCommandName == commandName {
			return agent, true
		}
	}
	return connectableAgent{}, false
}

func connectableAgentNames() []string {
	names := make([]string, 0, len(connectableAgents))
	for _, agent := range connectableAgents {
		names = append(names, agent.agentCommandName)
	}
	return names
}

func detectInstalledLocalAgents(home string) map[string]bool {
	detected := make(map[string]bool, len(connectableAgents))
	detected["claude-code"] = binaryOnPath("claude") || pathExists(filepath.Join(home, ".claude")) || pathExists(filepath.Join(home, ".claude.json"))
	detected["claude-desktop"] = pathExists(claudeDesktopConfigDir(home)) || (runtime.GOOS == "darwin" && pathExists("/Applications/Claude.app"))
	detected["codex"] = binaryOnPath("codex") || pathExists(filepath.Join(home, ".codex"))
	detected["executor"] = binaryOnPath("executor") || pathExists(filepath.Join(home, ".executor")) || pathExists(filepath.Join(home, ".config", "executor"))
	detected["openclaw"] = binaryOnPath("openclaw") || pathExists(filepath.Join(home, ".openclaw"))
	detected["hermes"] = binaryOnPath("hermes") || pathExists(filepath.Join(home, ".hermes"))
	detected["cursor"] = binaryOnPath("cursor") || pathExists(filepath.Join(home, ".cursor")) || (runtime.GOOS == "darwin" && pathExists("/Applications/Cursor.app"))
	detected["t3"] = pathExists(filepath.Join(home, ".t3")) || (runtime.GOOS == "darwin" && pathExists("/Applications/T3 Code.app"))
	return detected
}

func binaryOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func claudeDesktopConfigDir(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Claude")
	}
	return filepath.Join(home, ".config", "Claude")
}

func agentConfigFilePath(agentCommandName, home string) string {
	switch agentCommandName {
	case "claude-code":
		return filepath.Join(home, ".claude.json")
	case "claude-desktop":
		return filepath.Join(claudeDesktopConfigDir(home), "claude_desktop_config.json")
	case "codex":
		return filepath.Join(home, ".codex", "config.toml")
	case "executor":
		if pathExists(filepath.Join(home, ".executor")) {
			return filepath.Join(home, ".executor", "mcp.json")
		}
		return filepath.Join(home, ".config", "executor", "mcp.json")
	case "openclaw":
		return filepath.Join(home, ".openclaw", "mcp.json")
	case "hermes":
		return filepath.Join(home, ".hermes", "config.yaml")
	case "cursor":
		return filepath.Join(home, ".cursor", "mcp.json")
	default:
		return filepath.Join(home, ".config", "opentrawl", agentCommandName+".json")
	}
}

func canonicalOpenTrawlSkillPath(home string) string {
	return filepath.Join(home, ".agents", "skills", "opentrawl", "SKILL.md")
}

func canonicalSkillStatusFact(skillPath string) string {
	existing, err := os.ReadFile(skillPath)
	if errors.Is(err, fs.ErrNotExist) {
		return skillPath + " (not installed; pass --skill --write to install)"
	}
	if err != nil {
		return skillPath + " (unreadable)"
	}
	if string(existing) == canonicalOpenTrawlSkillMarkdown {
		return skillPath + " (canonical version installed)"
	}
	return skillPath + " (custom file installed; pass --skill to inspect diff)"
}

func resolveTrawlExecutableForMCPConfig() string {
	if binaryOnPath("trawl") {
		return "trawl"
	}
	if runtime.GOOS == "darwin" && pathExists(macOSAppBundledTrawlPath) {
		return macOSAppBundledTrawlPath
	}
	return "trawl"
}

func resolveConnectHTTPServerURL(ctx context.Context, port int) (string, string) {
	host := "127.0.0.1"
	hostSource := "loopback"
	if tailscaleHost := detectTailscaleMagicDNSHostName(ctx); tailscaleHost != "" {
		host = tailscaleHost
		hostSource = "Tailscale MagicDNS"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/mcp", hostSource
}

func connectModeLabel(write bool) string {
	if write {
		return "write"
	}
	return "dry-run preview (pass --write to apply)"
}

func connectAgentDetectionLabel(detected bool) string {
	if detected {
		return "detected"
	}
	return "not detected locally"
}

func buildOrApplyAgentMCPConfiguration(
	stateRoot string,
	agent connectableAgent,
	detected bool,
	configPath string,
	sources string,
	connection agentMCPConnection,
	write bool,
) (render.AgentConnectionConfigurationPresentation, error) {
	existingBytes, readErr := os.ReadFile(configPath)
	fileExisted := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return render.AgentConnectionConfigurationPresentation{}, readErr
	}

	oldSection, err := extractExistingOpentrawlSection(agent.configFormat, existingBytes)
	if err != nil {
		return render.AgentConnectionConfigurationPresentation{}, err
	}

	tokenID := ""
	displayConnection := connection
	if write {
		tokenRecord, bearerToken, tokenErr := createAgentAccessToken(stateRoot, agent.agentCommandName, sources, time.Now())
		if tokenErr != nil {
			return render.AgentConnectionConfigurationPresentation{}, tokenErr
		}
		tokenID = string(tokenRecord.AgentAccessTokenIdentifier)
		connection.bearerToken = bearerToken
		displayConnection.bearerToken = "<minted token " + tokenID + ">"
	}

	newSectionForDisplay := renderOpentrawlSection(agent.configFormat, displayConnection)
	diffLines := buildUnifiedSectionDiff(configPath, redactBearerTokensInLines(oldSection), newSectionForDisplay)

	if write {
		updatedBytes, applyErr := applyOpentrawlSection(agent.configFormat, existingBytes, connection)
		if applyErr != nil {
			return render.AgentConnectionConfigurationPresentation{}, applyErr
		}
		if writeErr := writeFileAtomically(configPath, updatedBytes, 0o600); writeErr != nil {
			return render.AgentConnectionConfigurationPresentation{}, writeErr
		}
	}

	status := connectAgentDetectionLabel(detected)
	if fileExisted && strings.TrimSpace(oldSection) != "" {
		if write {
			status = "updated"
		} else {
			status += ", existing opentrawl entry will be replaced"
		}
	} else if write {
		status = "configured"
	}

	return render.AgentConnectionConfigurationPresentation{
		AgentDisplayName:                   agent.agentDisplayName,
		AgentDetectedLocally:               detected,
		AgentConnectionConfigurationPath:   configPath,
		AgentConnectionConfigurationWasSet: write,
		AgentConnectionStatusSentence:      status,
		AgentConnectionTokenIdentifier:     tokenID,
		AgentConnectionDiffLines:           diffLines,
	}, nil
}

func buildOrApplyCanonicalSkill(skillPath string, write bool) (render.AgentConnectionConfigurationPresentation, error) {
	existingBytes, err := os.ReadFile(skillPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return render.AgentConnectionConfigurationPresentation{}, err
	}
	existingText := string(existingBytes)
	if existingText == canonicalOpenTrawlSkillMarkdown {
		return render.AgentConnectionConfigurationPresentation{
			AgentDisplayName:                   "Canonical SKILL.md",
			AgentDetectedLocally:               true,
			AgentConnectionConfigurationPath:   skillPath,
			AgentConnectionConfigurationWasSet: false,
			AgentConnectionStatusSentence:      "already up to date",
		}, nil
	}
	diffLines := buildUnifiedSectionDiff(skillPath, existingText, canonicalOpenTrawlSkillMarkdown)
	status := "will write canonical skill"
	if len(existingBytes) > 0 {
		status = "differs from existing skill"
	}
	if write {
		if err := writeFileAtomically(skillPath, []byte(canonicalOpenTrawlSkillMarkdown), 0o644); err != nil {
			return render.AgentConnectionConfigurationPresentation{}, err
		}
		status = "installed"
	}
	return render.AgentConnectionConfigurationPresentation{
		AgentDisplayName:                   "Canonical SKILL.md",
		AgentDetectedLocally:               len(existingBytes) > 0,
		AgentConnectionConfigurationPath:   skillPath,
		AgentConnectionConfigurationWasSet: write,
		AgentConnectionStatusSentence:      status,
		AgentConnectionDiffLines:           diffLines,
	}, nil
}

func extractExistingOpentrawlSection(format connectableAgentConfigFormat, existing []byte) (string, error) {
	if len(existing) == 0 {
		return "", nil
	}
	switch format {
	case connectableAgentConfigFormatJSON:
		var root map[string]json.RawMessage
		if err := json.Unmarshal(existing, &root); err != nil {
			return "", errors.New("existing JSON configuration is not valid JSON, so it was not changed")
		}
		rawServers, hasServers := root["mcpServers"]
		if !hasServers {
			return "", nil
		}
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			return "", errors.New("existing mcpServers value is not a JSON object, so it was not changed")
		}
		rawEntry, exists := servers["opentrawl"]
		if !exists {
			return "", nil
		}
		var pretty json.RawMessage
		if err := json.Unmarshal(rawEntry, &pretty); err != nil {
			return "", nil
		}
		wrapped := map[string]map[string]json.RawMessage{
			"mcpServers": {"opentrawl": pretty},
		}
		encoded, err := marshalIndentedJSONWithoutHTMLEscape(wrapped)
		if err != nil {
			return "", err
		}
		return string(encoded), nil

	case connectableAgentConfigFormatCodexTOML:
		start, end, found := findTOMLTableRange(string(existing), "[mcp_servers.opentrawl]")
		if !found {
			return "", nil
		}
		return strings.TrimRight(string(existing)[start:end], "\n") + "\n", nil

	case connectableAgentConfigFormatHermesYAML:
		section, found := extractYAMLOpentrawlMCPBlock(string(existing))
		if !found {
			return "", nil
		}
		return section, nil

	default:
		return "", nil
	}
}

func renderOpentrawlSection(format connectableAgentConfigFormat, connection agentMCPConnection) string {
	switch format {
	case connectableAgentConfigFormatJSON:
		rawEntry := marshalOpentrawlJSONEntry(connection)
		wrapped := map[string]map[string]json.RawMessage{
			"mcpServers": {"opentrawl": rawEntry},
		}
		encoded, _ := marshalIndentedJSONWithoutHTMLEscape(wrapped)
		return string(encoded)
	case connectableAgentConfigFormatCodexTOML:
		return codexConfigurationText(connection)
	case connectableAgentConfigFormatHermesYAML:
		return hermesConfigurationText(connection)
	default:
		return ""
	}
}

func applyOpentrawlSection(format connectableAgentConfigFormat, existing []byte, connection agentMCPConnection) ([]byte, error) {
	switch format {
	case connectableAgentConfigFormatJSON:
		return mergeMCPServersJSONBytes(existing, connection)
	case connectableAgentConfigFormatCodexTOML:
		return upsertCodexMCPServerBytes(existing, codexConfigurationText(connection)), nil
	case connectableAgentConfigFormatHermesYAML:
		return upsertHermesMCPServerBytes(existing, hermesConfigurationText(connection)), nil
	default:
		return nil, errors.New("unsupported configuration format")
	}
}

func marshalOpentrawlJSONEntry(connection agentMCPConnection) json.RawMessage {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if connection.transport == "stdio" {
		_ = encoder.Encode(opentrawlStdioMCPServerEntry{
			Command: connection.trawlExecutable,
			Args:    []string{"mcp"},
			Env:     map[string]string{agentAccessTokenEnvironmentKey: connection.bearerToken},
		})
		return json.RawMessage(bytes.TrimSpace(encoded.Bytes()))
	}
	_ = encoder.Encode(opentrawlHTTPMCPServerEntry{
		URL:     connection.serverURL,
		Headers: map[string]string{"Authorization": "Bearer " + connection.bearerToken},
	})
	return json.RawMessage(bytes.TrimSpace(encoded.Bytes()))
}

func mergeMCPServersJSONBytes(existing []byte, connection agentMCPConnection) ([]byte, error) {
	root := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, errors.New("existing JSON configuration is not valid JSON, so it was not changed")
		}
	}
	servers := map[string]json.RawMessage{}
	if rawServers, exists := root["mcpServers"]; exists && len(rawServers) > 0 {
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			return nil, errors.New("existing mcpServers value is not a JSON object, so it was not changed")
		}
	}
	servers["opentrawl"] = marshalOpentrawlJSONEntry(connection)
	encodedServers, err := json.Marshal(servers)
	if err != nil {
		return nil, err
	}
	root["mcpServers"] = json.RawMessage(encodedServers)
	return marshalIndentedJSONWithoutHTMLEscape(root)
}

func marshalIndentedJSONWithoutHTMLEscape[T map[string]json.RawMessage | map[string]map[string]json.RawMessage](value T) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func codexConfigurationText(connection agentMCPConnection) string {
	if connection.transport == "stdio" {
		return "[mcp_servers.opentrawl]\ncommand = " + strconv.Quote(connection.trawlExecutable) + "\nargs = [\"mcp\"]\nenv = { " + agentAccessTokenEnvironmentKey + " = " + strconv.Quote(connection.bearerToken) + " }\n"
	}
	return "[mcp_servers.opentrawl]\nurl = " + strconv.Quote(connection.serverURL) + "\nhttp_headers = { Authorization = " + strconv.Quote("Bearer "+connection.bearerToken) + " }\n"
}

func findTOMLTableRange(text, header string) (int, int, bool) {
	lines := strings.SplitAfter(text, "\n")
	offset := 0
	start := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if start == -1 {
			if trimmed == header {
				start = offset
			}
		} else if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			return start, offset, true
		}
		offset += len(line)
	}
	if start >= 0 {
		return start, len(text), true
	}
	return 0, 0, false
}

func upsertCodexMCPServerBytes(existing []byte, table string) []byte {
	text := string(existing)
	if start, end, found := findTOMLTableRange(text, "[mcp_servers.opentrawl]"); found {
		prefix := text[:start]
		suffix := text[end:]
		if suffix != "" && !strings.HasPrefix(suffix, "\n") {
			table += "\n"
		}
		return []byte(prefix + table + suffix)
	}
	if len(text) == 0 {
		return []byte(table)
	}
	separator := "\n"
	if !strings.HasSuffix(text, "\n") {
		separator = "\n\n"
	}
	return []byte(text + separator + table)
}

func hermesConfigurationText(connection agentMCPConnection) string {
	if connection.transport == "stdio" {
		return "mcp_servers:\n  opentrawl:\n    command: " + connection.trawlExecutable + "\n    args: [mcp]\n    env:\n      " + agentAccessTokenEnvironmentKey + ": " + strconv.Quote(connection.bearerToken) + "\n"
	}
	return "mcp_servers:\n  opentrawl:\n    url: " + strconv.Quote(connection.serverURL) + "\n    headers:\n      Authorization: " + strconv.Quote("Bearer "+connection.bearerToken) + "\n"
}

func extractYAMLOpentrawlMCPBlock(text string) (string, bool) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	inServers := false
	collecting := false
	var collected []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if collecting {
				break
			}
			inServers = trimmed == "mcp_servers:"
			continue
		}
		if !inServers {
			continue
		}
		if !collecting {
			if strings.HasPrefix(line, "  opentrawl:") {
				collecting = true
				collected = append(collected, "mcp_servers:", line)
			}
			continue
		}
		if trimmed != "" && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "\t") {
			break
		}
		collected = append(collected, line)
	}
	if !collecting {
		return "", false
	}
	return strings.Join(collected, "\n") + "\n", true
}

func upsertHermesMCPServerBytes(existing []byte, yamlBlock string) []byte {
	text := string(existing)
	if len(strings.TrimSpace(text)) == 0 {
		return []byte(yamlBlock)
	}
	entryLines := strings.Split(strings.TrimRight(yamlBlock, "\n"), "\n")[1:]
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var out []string
	inServers := false
	replaced := false
	skippingExisting := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isTopLevel := !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" && !strings.HasPrefix(trimmed, "#")
		if isTopLevel {
			if inServers && !replaced {
				out = append(out, entryLines...)
				replaced = true
			}
			skippingExisting = false
			inServers = trimmed == "mcp_servers:"
			out = append(out, line)
			continue
		}
		if inServers {
			if skippingExisting {
				if trimmed == "" || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
					continue
				}
				skippingExisting = false
			}
			if strings.HasPrefix(line, "  opentrawl:") {
				out = append(out, entryLines...)
				replaced = true
				skippingExisting = true
				continue
			}
		}
		out = append(out, line)
	}
	if inServers && !replaced {
		out = append(out, entryLines...)
		replaced = true
	}
	if !replaced {
		out = append(out, "", strings.TrimRight(yamlBlock, "\n"))
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

func redactBearerTokensInLines(text string) string {
	if !strings.Contains(text, agentAccessBearerTokenPrefix) {
		return text
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		for {
			pos := strings.Index(line, agentAccessBearerTokenPrefix)
			if pos < 0 {
				break
			}
			end := pos + len(agentAccessBearerTokenPrefix)
			for end < len(line) && isBearerTokenChar(line[end]) {
				end++
			}
			line = line[:pos] + "<existing-token>" + line[end:]
		}
		lines[index] = line
	}
	return strings.Join(lines, "\n")
}

func isBearerTokenChar(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_'
}

func buildUnifiedSectionDiff(path, beforeText, afterText string) []string {
	beforeLines := splitNonEmptyTrailingLines(beforeText)
	afterLines := splitNonEmptyTrailingLines(afterText)
	if strings.Join(beforeLines, "\n") == strings.Join(afterLines, "\n") {
		return nil
	}
	diff := []string{
		"--- " + path,
		"+++ " + path,
	}
	for _, line := range beforeLines {
		diff = append(diff, "- "+line)
	}
	for _, line := range afterLines {
		diff = append(diff, "+ "+line)
	}
	return diff
}

func splitNonEmptyTrailingLines(text string) []string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func writeFileAtomically(path string, contents []byte, permissions fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(permissions); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
