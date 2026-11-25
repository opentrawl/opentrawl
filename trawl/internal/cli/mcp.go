package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// McpCmd serves the read-only OpenTrawl MCP tools over stdio (default),
// local/Tailscale Streamable HTTP (--http), or the blind SNI-passthrough
// internet relay (--relay) with Mac-side CertMagic TLS and built-in OAuth 2.1.
type McpCmd struct {
	Sources       string        `name:"sources" placeholder:"TRAWLERS" help:"Sources the agent may read in stdio mode, separated by commas; ignored when OPENTRAWL_ACCESS_TOKEN names a token"`
	HTTP          bool          `name:"http" help:"Serve Streamable HTTP MCP and remote archive endpoints instead of stdio"`
	Tailscale     bool          `name:"tailscale" help:"Bind --http exclusively to this machine's Tailscale WireGuard IP (never Wi-Fi or LAN)"`
	Listen        string        `name:"listen" default:"127.0.0.1:7391" placeholder:"HOST:PORT" help:"Loopback or Tailscale IP address to listen on with --http"`
	TLSCert       string        `name:"tls-cert" placeholder:"FILE" help:"PEM TLS certificate file for HTTPS with --http (pair with --tls-key)"`
	TLSKey        string        `name:"tls-key" placeholder:"FILE" help:"PEM TLS private key file for HTTPS with --http (pair with --tls-cert)"`
	Relay         bool          `name:"relay" help:"Expose HTTPS MCP over OpenTrawl's blind SNI-passthrough relay with Mac-side TLS and OAuth 2.1"`
	Pair          bool          `name:"pair" help:"Open a one-time OAuth pairing window on startup and print the pairing code"`
	PairWindow    time.Duration `name:"pair-window" default:"5m" help:"How long the one-time OAuth pairing window stays open"`
	RelayServer   string        `name:"relay-server" default:"relay.mcp.opentrawl.sh:7000" placeholder:"HOST:PORT" help:"Relay frps control address for --relay"`
	RelayDomain   string        `name:"relay-domain" default:"mcp.opentrawl.sh" placeholder:"DOMAIN" help:"Base relay domain for --relay"`
	RelayCAFile   string        `name:"relay-ca-file" hidden:"" help:"PEM CA bundle for verifying the relay control server certificate"`
	ACMECA        string        `name:"acme-ca" hidden:"" help:"Override ACME directory URL for testing --relay"`
	ACMECARoots   string        `name:"acme-ca-roots" hidden:"" help:"PEM root CA bundle for --acme-ca"`
	RelayPlainTCP bool          `name:"relay-no-control-tls" hidden:"" help:"Disable TLS on frpc control channel (local testing only)"`
}

const (
	mcpServerName                  = "opentrawl"
	mcpDefaultProtocolVersion      = "2025-06-18"
	mcpJSONRPCVersion              = "2.0"
	mcpErrorCodeParse              = -32700
	mcpErrorCodeInvalidRequest     = -32600
	mcpErrorCodeMethodNotFound     = -32601
	mcpErrorCodeInvalidParameters  = -32602
	mcpMaximumStandardInputMessage = 16 << 20
	mcpMaximumHTTPBodyByteCount    = 16 << 20
	mcpMaximumRemoteQueryLimit     = 200
)

type mcpJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *mcpJSONRPCError `json:"error,omitempty"`
}

type mcpToolJSONSchemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Minimum     int    `json:"minimum,omitempty"`
}

type mcpToolInputSchema struct {
	Type       string                               `json:"type"`
	Required   []string                             `json:"required,omitempty"`
	Properties map[string]mcpToolJSONSchemaProperty `json:"properties"`
}

type mcpToolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

type mcpToolDefinition struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	InputSchema mcpToolInputSchema `json:"inputSchema"`
	Annotations mcpToolAnnotations `json:"annotations"`
}

type mcpInitializeCapabilities struct {
	Tools      mcpInitializeToolsCapability     `json:"tools"`
	Resources  mcpInitializeResourcesCapability `json:"resources"`
	Extensions *mcpInitializeExtensions         `json:"extensions,omitempty"`
}

type mcpInitializeSkillsExtension struct{}

type mcpInitializeExtensions struct {
	Skills *mcpInitializeSkillsExtension `json:"io.modelcontextprotocol/skills,omitempty"`
}

type mcpInitializeToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type mcpInitializeResourcesCapability struct {
	Subscribe   bool `json:"subscribe"`
	ListChanged bool `json:"listChanged"`
}

type mcpServerImplementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpInitializeResult struct {
	ProtocolVersion string                      `json:"protocolVersion"`
	Capabilities    mcpInitializeCapabilities   `json:"capabilities"`
	ServerInfo      mcpServerImplementationInfo `json:"serverInfo"`
	Instructions    string                      `json:"instructions"`
}

type mcpToolsListResult struct {
	Tools []mcpToolDefinition `json:"tools"`
}

type mcpToolContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

type mcpToolCallResult struct {
	Content           []mcpToolContentBlock `json:"content"`
	StructuredContent json.RawMessage       `json:"structuredContent,omitempty"`
	IsError           bool                  `json:"isError"`
}

type mcpHTTPErrorResponse struct {
	Error string `json:"error"`
}

type mcpHealthzResponse struct {
	Status string `json:"status"`
}

// mcpToolArguments holds every tool's arguments; each tool reads its own.
type mcpToolArguments struct {
	Query   string `json:"query"`
	Trawler string `json:"trawler"`
	Limit   int    `json:"limit"`
	After   string `json:"after"`
	Before  string `json:"before"`
	Who     string `json:"who"`
	Link    string `json:"link"`
	Target  string `json:"target"`
	Trip    string `json:"trip"`
	Window  string `json:"window"`
	Name    string `json:"name"`
}

func mcpStringProperty(description string) mcpToolJSONSchemaProperty {
	return mcpToolJSONSchemaProperty{Type: "string", Description: description}
}

func mcpToolDefinitions() []mcpToolDefinition {
	limitProperty := mcpToolJSONSchemaProperty{Type: "integer", Minimum: 1, Description: "Maximum number of results."}
	readOnlyArchiveAnnotations := mcpToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	}
	return []mcpToolDefinition{
		{
			Name:        "trawl_search",
			Description: "Search the user's own archives (messages, email, calendar, notes, photos, money and more) in one ranked list. Each result has a link to pass to trawl_open or trawl_around.",
			InputSchema: mcpToolInputSchema{
				Type: "object",
				Properties: map[string]mcpToolJSONSchemaProperty{
					"query":   mcpStringProperty("Words to find."),
					"trawler": mcpStringProperty("Only these sources, separated by commas, such as gmail,imessage."),
					"limit":   limitProperty,
					"after":   mcpStringProperty("Results on or after this date, YYYY-MM-DD."),
					"before":  mcpStringProperty("Results on or before this date, YYYY-MM-DD."),
					"who":     mcpStringProperty("Results that involve this person."),
				},
			},
			Annotations: readOnlyArchiveAnnotations,
		},
		{
			Name:        "trawl_open",
			Description: "Open one record by the link from a search or list, and read it in full.",
			InputSchema: mcpToolInputSchema{
				Type:     "object",
				Required: []string{"link"},
				Properties: map[string]mcpToolJSONSchemaProperty{
					"link": mcpStringProperty("Link from trawl_search, such as gmail:abc123."),
				},
			},
			Annotations: readOnlyArchiveAnnotations,
		},
		{
			Name:        "trawl_around",
			Description: "Find records from every allowed source near a record, date, or trip: what else happened around then.",
			InputSchema: mcpToolInputSchema{
				Type: "object",
				Properties: map[string]mcpToolJSONSchemaProperty{
					"target":  mcpStringProperty("A link from trawl_search, or a date YYYY-MM-DD."),
					"trip":    mcpStringProperty("Indexed trip or place window name."),
					"window":  mcpStringProperty("Time either side, such as 12h, 3d or 2w. Default 3d."),
					"trawler": mcpStringProperty("Only these sources, separated by commas."),
					"limit":   limitProperty,
				},
			},
			Annotations: readOnlyArchiveAnnotations,
		},
		{
			Name:        "trawl_status",
			Description: "Show each allowed source: what is archived, when it was last updated and whether it works.",
			InputSchema: mcpToolInputSchema{
				Type: "object",
				Properties: map[string]mcpToolJSONSchemaProperty{
					"trawler": mcpStringProperty("One source name."),
				},
			},
			Annotations: readOnlyArchiveAnnotations,
		},
		{
			Name:        "trawl_who",
			Description: "Find a person by name, phone number or email address across allowed sources.",
			InputSchema: mcpToolInputSchema{
				Type:     "object",
				Required: []string{"name"},
				Properties: map[string]mcpToolJSONSchemaProperty{
					"name":  mcpStringProperty("Name, phone number or email address."),
					"limit": limitProperty,
				},
			},
			Annotations: readOnlyArchiveAnnotations,
		},
	}
}

func mcpResultResponse(id json.RawMessage, payload json.RawMessage) *mcpJSONRPCResponse {
	return &mcpJSONRPCResponse{JSONRPC: mcpJSONRPCVersion, ID: mcpResponseIdentifier(id), Result: payload}
}

func mcpErrorResponse(id json.RawMessage, code int, message string) *mcpJSONRPCResponse {
	return &mcpJSONRPCResponse{JSONRPC: mcpJSONRPCVersion, ID: mcpResponseIdentifier(id), Error: &mcpJSONRPCError{Code: code, Message: message}}
}

func mcpResponseIdentifier(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// resolveScope validates credentials for one MCP call. When a bearer token is
// active, it re-reads the token store so revocations apply immediately.
type mcpScopeResolver func() (agentSourceScope, error)

// handleMCPMessage answers one JSON-RPC message. It returns nil for a
// notification, which gets no response. Both session-era (initialize first)
// and stateless-era (direct tools/list and tools/call) MCP clients work.
func (service *agentArchiveService) handleMCPMessage(ctx context.Context, resolveScope mcpScopeResolver, encodedMessage []byte) *mcpJSONRPCResponse {
	var request mcpJSONRPCRequest
	if err := json.Unmarshal(encodedMessage, &request); err != nil {
		return mcpErrorResponse(nil, mcpErrorCodeParse, "message is not JSON-RPC: "+err.Error())
	}
	if request.JSONRPC != mcpJSONRPCVersion || request.Method == "" {
		return mcpErrorResponse(request.ID, mcpErrorCodeInvalidRequest, "message must be JSON-RPC 2.0 with a method")
	}
	if len(request.ID) == 0 {
		return nil
	}
	switch request.Method {
	case "initialize":
		var parameters struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &parameters)
		protocolVersion := strings.TrimSpace(parameters.ProtocolVersion)
		if protocolVersion == "" {
			protocolVersion = mcpDefaultProtocolVersion
		}
		encoded, err := json.Marshal(mcpInitializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities: mcpInitializeCapabilities{
				Tools:      mcpInitializeToolsCapability{ListChanged: false},
				Resources:  mcpInitializeResourcesCapability{Subscribe: false, ListChanged: false},
				Extensions: &mcpInitializeExtensions{Skills: &mcpInitializeSkillsExtension{}},
			},
			ServerInfo:   mcpServerImplementationInfo{Name: mcpServerName, Version: Version},
			Instructions: "OpenTrawl searches the user's own archives locally and read-only. Start with trawl_search, then trawl_open a result's link. This connection can read only the sources trawl_status lists. Read skill://opentrawl/SKILL.md for archive usage guidance and skill://personal-wiki/SKILL.md when building or refreshing a personal wiki.",
		})
		if err != nil {
			return mcpErrorResponse(request.ID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
		}
		return mcpResultResponse(request.ID, encoded)
	case "ping":
		return mcpResultResponse(request.ID, json.RawMessage("{}"))
	case "tools/list":
		encoded, err := json.Marshal(mcpToolsListResult{Tools: mcpToolDefinitions()})
		if err != nil {
			return mcpErrorResponse(request.ID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
		}
		return mcpResultResponse(request.ID, encoded)
	case "skills/list":
		return handleMCPSkillsList(request.ID)
	case "skills/get":
		return handleMCPSkillsGet(request.ID, request.Params)
	case "resources/list":
		return handleMCPResourcesList(request.ID)
	case "resources/read":
		return handleMCPResourcesRead(request.ID, request.Params)
	case "tools/call":
		scope, err := resolveScope()
		if err != nil {
			encoded, marshalErr := json.Marshal(mcpErrorToolCallResult(err.Error()))
			if marshalErr != nil {
				return mcpErrorResponse(request.ID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+marshalErr.Error())
			}
			return mcpResultResponse(request.ID, encoded)
		}
		var parameters struct {
			Name      string           `json:"name"`
			Arguments mcpToolArguments `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &parameters); err != nil {
			return mcpErrorResponse(request.ID, mcpErrorCodeInvalidParameters, "tool arguments are not valid: "+err.Error())
		}
		toolResult, known := service.executeMCPToolCall(ctx, scope, parameters.Name, parameters.Arguments)
		if !known {
			return mcpErrorResponse(request.ID, mcpErrorCodeInvalidParameters, "unknown tool "+parameters.Name)
		}
		encoded, err := json.Marshal(toolResult)
		if err != nil {
			return mcpErrorResponse(request.ID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
		}
		return mcpResultResponse(request.ID, encoded)
	default:
		return mcpErrorResponse(request.ID, mcpErrorCodeMethodNotFound, "method not found: "+request.Method)
	}
}

func (c *McpCmd) Run(r *Runtime) error {
	if c.Relay {
		return c.runRelay(r)
	}
	if c.HTTP {
		return c.runHTTP(r)
	}
	return c.runStdio(r)
}

func (c *McpCmd) runStdio(r *Runtime) error {
	bearerToken := strings.TrimSpace(os.Getenv(agentAccessTokenEnvironmentKey))
	var resolveScope mcpScopeResolver
	if bearerToken != "" {
		if _, err := findActiveAgentAccessToken(r.stateRoot, bearerToken); err != nil {
			return render.AgentAccessCommandIsWrong(agentAccessTokenEnvironmentKey + ": " + err.Error())
		}
		resolveScope = func() (agentSourceScope, error) {
			token, err := findActiveAgentAccessToken(r.stateRoot, bearerToken)
			if err != nil {
				return agentSourceScope{}, err
			}
			return token.agentSourceScope(), nil
		}
	} else {
		allSources, sources := parseAllowedSourcesList(c.Sources)
		fixedScope := newAgentSourceScope("stdio", allSources, sources)
		resolveScope = func() (agentSourceScope, error) {
			return fixedScope, nil
		}
	}
	service := newAgentArchiveService(r.ctx, r.stateRoot)
	return serveMCPOverLines(r.ctx, service, resolveScope, os.Stdin, r.machineReadableProtocolOutputStream)
}

func (c *McpCmd) runRelay(r *Runtime) error {
	service := newAgentArchiveService(r.ctx, r.stateRoot)
	sourceNames := discoveredSourceNames(service)
	relayOrigin, err := resolveRelayDeviceOrigin(r.stateRoot, c.RelayDomain)
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot resolve relay device identity: " + err.Error())
	}
	oauthServer := newRemoteMCPOAuthServer(r.stateRoot, relayOrigin, agentAccessTokenSurfaceRelay, sourceNames)
	var pairingCode string
	var pairingExpires time.Time
	if c.Pair {
		code, expiresAt, err := oauthServer.OpenPairingWindow(c.PairWindow)
		if err != nil {
			return render.AgentAccessOperationFailed("Cannot open pairing window: " + err.Error())
		}
		pairingCode = code
		pairingExpires = expiresAt
	}
	handler := &agentMCPHTTPHandler{
		service:              service,
		oauth:                oauthServer,
		surface:              agentAccessTokenSurfaceRelay,
		allowRemoteAppRoutes: false,
	}

	tunnelCtx, stopSignals := signal.NotifyContext(r.ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	tunnel, err := startRemoteMCPRelayTunnel(tunnelCtx, remoteMCPRelayLaunchOptions{
		StateRoot:             r.stateRoot,
		RelayServerAddress:    c.RelayServer,
		RelayBaseDomain:       c.RelayDomain,
		RelayTrustedCAFile:    c.RelayCAFile,
		ACMEDirectoryURL:      c.ACMECA,
		ACMETrustedRootsPEM:   c.ACMECARoots,
		DisableRelayClientTLS: c.RelayPlainTCP,
	}, handler)
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot start remote MCP relay: " + err.Error())
	}
	defer tunnel.Close()

	facts := []render.FieldValueRow{
		{FieldLabel: "MCP", FieldValue: tunnel.PublicOrigin + "/mcp"},
		{FieldLabel: "TLS", FieldValue: "terminated on this Mac (CertMagic TLS-ALPN-01)"},
		{FieldLabel: "sources", FieldValue: strings.Join(sourceNames, ", ")},
	}
	if pairingCode != "" {
		facts = append(facts, render.FieldValueRow{
			FieldLabel: "pair code",
			FieldValue: pairingCode + " (expires " + pairingExpires.Local().Format("15:04:05 -07:00") + ")",
		})
	} else {
		facts = append(facts, render.FieldValueRow{
			FieldLabel: "pairing",
			FieldValue: "closed (pass --pair to connect a new OAuth agent)",
		})
	}
	if err := r.output.WriteAgentMCPServerListening(render.AgentMCPServerListeningPresentation{
		AgentMCPServerListeningSentence: "OpenTrawl remote MCP server is live over HTTPS. TLS ends on this Mac; every request requires OAuth 2.1 or a scoped bearer token.",
		AgentMCPServerFacts:             facts,
		NextTrawlCommands:               [][]string{{"token", "list"}},
	}); err != nil {
		return err
	}

	<-tunnelCtx.Done()
	return nil
}

func (c *McpCmd) runHTTP(r *Runtime) error {
	tlsCertPath := strings.TrimSpace(c.TLSCert)
	tlsKeyPath := strings.TrimSpace(c.TLSKey)
	if (tlsCertPath == "") != (tlsKeyPath == "") {
		return render.AgentAccessCommandIsWrong("pass both --tls-cert and --tls-key to serve HTTPS")
	}
	useTLS := tlsCertPath != ""

	service := newAgentArchiveService(r.ctx, r.stateRoot)
	listenAddress := strings.TrimSpace(c.Listen)
	if c.Tailscale {
		if service.tailscaleIPv4Address == "" {
			return render.AgentAccessOperationFailed("Tailscale is not running on this machine; start Tailscale or omit --tailscale to listen on 127.0.0.1")
		}
		_, port, err := net.SplitHostPort(listenAddress)
		if err != nil || port == "" {
			port = "7391"
		}
		listenAddress = net.JoinHostPort(service.tailscaleIPv4Address, port)
	}
	if !useTLS {
		if err := validatePlainHTTPListenAddress(listenAddress, service.tailscaleIPv4Address); err != nil {
			return render.AgentAccessCommandIsWrong(err.Error())
		}
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot listen on " + listenAddress + ": " + err.Error())
	}
	defer func() { _ = listener.Close() }()

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	listenIP, port, _ := net.SplitHostPort(listener.Addr().String())
	advertisedHost := listenIP
	if parsedIP := net.ParseIP(listenIP); parsedIP == nil || !parsedIP.IsLoopback() {
		if service.tailscaleMagicDNSHostName != "" {
			advertisedHost = service.tailscaleMagicDNSHostName
		} else if service.tailscaleIPv4Address != "" {
			advertisedHost = service.tailscaleIPv4Address
		}
	}
	remoteArchiveBaseURL := scheme + "://" + net.JoinHostPort(advertisedHost, port)
	mcpEndpointURL := remoteArchiveBaseURL + "/mcp"

	sourceNames := discoveredSourceNames(service)
	oauthServer := newRemoteMCPOAuthServer(r.stateRoot, remoteArchiveBaseURL, agentAccessTokenSurfaceLocal, sourceNames)
	var pairingCode string
	var pairingExpires time.Time
	if c.Pair {
		code, expiresAt, err := oauthServer.OpenPairingWindow(c.PairWindow)
		if err != nil {
			return render.AgentAccessOperationFailed("Cannot open pairing window: " + err.Error())
		}
		pairingCode = code
		pairingExpires = expiresAt
	}

	facts := []render.FieldValueRow{
		{FieldLabel: "listening", FieldValue: scheme + "://" + listener.Addr().String()},
		{FieldLabel: "MCP", FieldValue: mcpEndpointURL},
		{FieldLabel: "remote archive", FieldValue: remoteArchiveBaseURL},
		{FieldLabel: "sources", FieldValue: strings.Join(sourceNames, ", ")},
	}
	if pairingCode != "" {
		facts = append(facts, render.FieldValueRow{
			FieldLabel: "pair code",
			FieldValue: pairingCode + " (expires " + pairingExpires.Local().Format("15:04:05 -07:00") + ")",
		})
	}
	if err := r.output.WriteAgentMCPServerListening(render.AgentMCPServerListeningPresentation{
		AgentMCPServerListeningSentence: "OpenTrawl HTTP server is listening for MCP and remote archive clients. Every request needs OAuth 2.1 or a scoped bearer token.",
		AgentMCPServerFacts:             facts,
		NextTrawlCommands:               [][]string{{"connect", "--transport", "http"}},
	}); err != nil {
		return err
	}

	serverCtx, stopSignals := signal.NotifyContext(r.ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	httpServer := &http.Server{
		Handler: &agentMCPHTTPHandler{
			service:              service,
			oauth:                oauthServer,
			surface:              agentAccessTokenSurfaceLocal,
			allowRemoteAppRoutes: true,
		},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-serverCtx.Done()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelShutdown()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	var serveErr error
	if useTLS {
		serveErr = httpServer.ServeTLS(listener, tlsCertPath, tlsKeyPath)
	} else {
		serveErr = httpServer.Serve(listener)
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return render.AgentAccessOperationFailed("MCP HTTP server stopped: " + serveErr.Error())
	}
	return nil
}

func discoveredSourceNames(service *agentArchiveService) []string {
	sourceNames := make([]string, 0, len(service.installedTrawlersDiscoveredAtServerStart))
	for _, trawler := range service.installedTrawlersDiscoveredAtServerStart {
		if trawler.TrawlerDiscoveryError == nil {
			sourceNames = append(sourceNames, installedTrawlerIdentityText(trawler))
		}
	}
	return sourceNames
}

// validatePlainHTTPListenAddress ensures unencrypted HTTP only binds to
// loopback or this machine's active Tailscale WireGuard IP, never 0.0.0.0 or
// an unencrypted Wi-Fi/LAN interface.
func validatePlainHTTPListenAddress(listenAddress string, activeTailscaleIPv4 string) error {
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return errors.New("listen address must be HOST:PORT, such as 127.0.0.1:7391")
	}
	trimmed := strings.TrimSpace(host)
	if strings.EqualFold(trimmed, "localhost") {
		return nil
	}
	parsedIP := net.ParseIP(trimmed)
	if parsedIP != nil && parsedIP.IsLoopback() {
		return nil
	}
	if activeTailscaleIPv4 != "" && trimmed == activeTailscaleIPv4 {
		return nil
	}
	return errors.New("refusing to serve unencrypted HTTP on " + listenAddress + "; pass --tailscale to bind only to your Tailscale WireGuard IP, listen on 127.0.0.1, pass --tls-cert and --tls-key, or use --relay")
}

type agentMCPHTTPHandler struct {
	service              *agentArchiveService
	oauth                *remoteMCPOAuthServer
	surface              agentAccessTokenSurface
	allowRemoteAppRoutes bool
}

func (handler *agentMCPHTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/healthz" {
		writeMCPJSONBytes(response, http.StatusOK, mustMarshalMCPJSON(mcpHealthzResponse{Status: "ok"}))
		return
	}
	if isRemoteMCPServerCardPath(request.URL.Path) {
		handler.handleRemoteMCPServerCard(response, request)
		return
	}
	if handler.oauth != nil && handler.oauth.isOAuthPath(request.URL.Path) {
		handler.oauth.ServeHTTP(response, request)
		return
	}
	switch request.URL.Path {
	case "/mcp":
		if !handler.isAllowedMCPRequestOrigin(request) {
			writeMCPHTTPError(response, http.StatusForbidden, "request Origin is not allowed")
			return
		}
		if request.Method == http.MethodDelete {
			if _, ok := handler.authenticateRequest(response, request); !ok {
				return
			}
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", "POST, DELETE")
			writeMCPHTTPError(response, http.StatusMethodNotAllowed, "MCP over HTTP takes POST; this server does not open server-sent event streams")
			return
		}
		scope, ok := handler.authenticateRequest(response, request)
		if !ok {
			return
		}
		if protocolVersion := strings.TrimSpace(request.Header.Get("MCP-Protocol-Version")); protocolVersion != "" {
			response.Header().Set("MCP-Protocol-Version", protocolVersion)
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, mcpMaximumHTTPBodyByteCount))
		if err != nil {
			writeMCPHTTPError(response, http.StatusBadRequest, err.Error())
			return
		}
		mcpResponse := handler.service.handleMCPMessage(request.Context(), func() (agentSourceScope, error) {
			return scope, nil
		}, body)
		if mcpResponse == nil {
			response.WriteHeader(http.StatusAccepted)
			return
		}
		writeMCPJSONBytes(response, http.StatusOK, mustMarshalMCPJSON(mcpResponse))
	case remoteAppStatusPath, remoteAppSearchPath, remoteAppOpenPath, remoteAppPhotoPath:
		if !handler.allowRemoteAppRoutes {
			writeMCPHTTPError(response, http.StatusNotFound, "no such endpoint; use POST /mcp")
			return
		}
		scope, ok := handler.authenticateRequest(response, request)
		if !ok {
			return
		}
		handler.handleRemoteAppRequest(response, request, scope)
	default:
		if handler.allowRemoteAppRoutes {
			writeMCPHTTPError(response, http.StatusNotFound, "no such endpoint; use POST /mcp or GET /app/status, /app/search, /app/open, /app/photo")
			return
		}
		writeMCPHTTPError(response, http.StatusNotFound, "no such endpoint; use POST /mcp")
	}
}

func (handler *agentMCPHTTPHandler) isAllowedMCPRequestOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsedOrigin, err := url.Parse(origin)
	if err != nil || parsedOrigin.Host == "" || parsedOrigin.Path != "" || parsedOrigin.RawQuery != "" || parsedOrigin.Fragment != "" {
		return false
	}
	if parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https" {
		return false
	}
	normalizedOrigin := strings.ToLower(parsedOrigin.Scheme) + "://" + strings.ToLower(parsedOrigin.Host)
	if handler.oauth != nil {
		expectedOrigin := strings.ToLower(strings.TrimRight(handler.oauth.publicOriginForRequest(request), "/"))
		if normalizedOrigin == expectedOrigin {
			return true
		}
	}
	// On the local HTTP listener only, also allow exact same-scheme, same-port
	// loopback origins (e.g., localhost vs 127.0.0.1 on the same listen port).
	if handler.surface == agentAccessTokenSurfaceRelay {
		return false
	}
	expectedScheme := "http"
	if request.TLS != nil {
		expectedScheme = "https"
	}
	if !strings.EqualFold(parsedOrigin.Scheme, expectedScheme) {
		return false
	}
	originHost := parsedOrigin.Hostname()
	originIsLoopback := strings.EqualFold(originHost, "localhost")
	if !originIsLoopback {
		if ip := net.ParseIP(originHost); ip != nil && ip.IsLoopback() {
			originIsLoopback = true
		}
	}
	if !originIsLoopback {
		return false
	}
	reqHost, reqPort, splitErr := net.SplitHostPort(request.Host)
	if splitErr != nil {
		reqHost = request.Host
		reqPort = ""
	}
	reqIsLoopback := strings.EqualFold(reqHost, "localhost")
	if !reqIsLoopback {
		if ip := net.ParseIP(reqHost); ip != nil && ip.IsLoopback() {
			reqIsLoopback = true
		}
	}
	return reqIsLoopback && parsedOrigin.Port() == reqPort
}

func (handler *agentMCPHTTPHandler) authenticateRequest(
	response http.ResponseWriter,
	request *http.Request,
) (agentSourceScope, bool) {
	writeUnauthorized := func(message string) (agentSourceScope, bool) {
		if handler.oauth != nil {
			response.Header().Set(
				"WWW-Authenticate",
				`Bearer resource_metadata="`+handler.oauth.protectedResourceMetadataURL(request)+`", scope="`+remoteMCPOAuthReadScope+`"`,
			)
		}
		writeMCPHTTPError(response, http.StatusUnauthorized, message)
		return agentSourceScope{}, false
	}
	bearerToken, hasBearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !hasBearer {
		return writeUnauthorized("send Authorization: Bearer <token> or sign in with OAuth 2.1")
	}
	token, err := findActiveAgentAccessToken(handler.service.stateRoot, bearerToken)
	if err != nil {
		return writeUnauthorized(errAgentAccessTokenNotRecognised.Error())
	}
	requiredSurface := handler.surface
	if requiredSurface == "" {
		requiredSurface = agentAccessTokenSurfaceLocal
	}
	if !token.allowsSurface(requiredSurface) {
		return writeUnauthorized(errAgentAccessTokenNotRecognised.Error())
	}
	return token.agentSourceScope(), true
}

func writeMCPHTTPError(response http.ResponseWriter, statusCode int, message string) {
	writeMCPJSONBytes(response, statusCode, mustMarshalMCPJSON(mcpHTTPErrorResponse{Error: message}))
}

func mustMarshalMCPJSON[T mcpHealthzResponse | mcpHTTPErrorResponse | *mcpJSONRPCResponse](value T) []byte {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return []byte(`{"error":"response could not be encoded"}` + "\n")
	}
	return encoded.Bytes()
}

func writeMCPJSONBytes(response http.ResponseWriter, statusCode int, payload []byte) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(statusCode)
	_, _ = response.Write(payload)
}

func serveMCPOverLines(ctx context.Context, service *agentArchiveService, resolveScope mcpScopeResolver, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), mcpMaximumStandardInputMessage)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if response := service.handleMCPMessage(ctx, resolveScope, []byte(line)); response != nil {
			if err := encoder.Encode(response); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func commandServesAgents(args []string) bool {
	for _, argument := range args {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		return argument == "mcp"
	}
	return false
}
