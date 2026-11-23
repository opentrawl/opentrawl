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
	"os"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// McpCmd serves the read-only OpenTrawl MCP tools over stdio (default) or
// Streamable HTTP (--http).
type McpCmd struct {
	Sources string `name:"sources" placeholder:"TRAWLERS" help:"Sources the agent may read in stdio mode, separated by commas; ignored when OPENTRAWL_ACCESS_TOKEN names a token"`
	HTTP    bool   `name:"http" help:"Serve Streamable HTTP MCP instead of stdio"`
	Listen  string `name:"listen" default:"127.0.0.1:7391" placeholder:"HOST:PORT" help:"Address to listen on with --http; use 0.0.0.0:7391 to serve your tailnet"`
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

type mcpToolDefinition struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	InputSchema mcpToolInputSchema `json:"inputSchema"`
}

type mcpInitializeCapabilities struct {
	Tools mcpInitializeToolsCapability `json:"tools"`
}

type mcpInitializeToolsCapability struct {
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

type mcpToolTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolCallResult struct {
	Content []mcpToolTextContent `json:"content"`
	IsError bool                 `json:"isError"`
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
// notification, which gets no response.
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
			Capabilities:    mcpInitializeCapabilities{Tools: mcpInitializeToolsCapability{ListChanged: false}},
			ServerInfo:      mcpServerImplementationInfo{Name: mcpServerName, Version: Version},
			Instructions:    "OpenTrawl searches the user's own archives locally and read-only. Start with trawl_search, then trawl_open a result's link. This connection can read only the sources trawl_status lists.",
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
	case "tools/call":
		scope, err := resolveScope()
		if err != nil {
			encoded, marshalErr := json.Marshal(mcpToolCallResult{
				Content: []mcpToolTextContent{{Type: "text", Text: err.Error()}},
				IsError: true,
			})
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
		run, known := service.mcpToolCommand(parameters.Name, parameters.Arguments)
		if !known {
			return mcpErrorResponse(request.ID, mcpErrorCodeInvalidParameters, "unknown tool "+parameters.Name)
		}
		text, failed := service.runTrawlCommandForAgent(ctx, scope, run)
		encoded, err := json.Marshal(mcpToolCallResult{
			Content: []mcpToolTextContent{{Type: "text", Text: text}},
			IsError: failed,
		})
		if err != nil {
			return mcpErrorResponse(request.ID, mcpErrorCodeInvalidRequest, "result could not be encoded: "+err.Error())
		}
		return mcpResultResponse(request.ID, encoded)
	default:
		return mcpErrorResponse(request.ID, mcpErrorCodeMethodNotFound, "method not found: "+request.Method)
	}
}

// mcpToolCommand maps a read-only MCP tool call to the trawl command a person
// would type.
func (service *agentArchiveService) mcpToolCommand(toolName string, arguments mcpToolArguments) (func(*Runtime) error, bool) {
	limitOrDefault := func(defaultLimit int) int {
		if arguments.Limit > 0 {
			return arguments.Limit
		}
		return defaultLimit
	}
	switch toolName {
	case "trawl_search":
		command := &SearchCmd{Query: strings.Fields(arguments.Query), Trawler: arguments.Trawler, Limit: limitOrDefault(20), After: arguments.After, Before: arguments.Before, Who: arguments.Who}
		return command.Run, true
	case "trawl_open":
		command := &OpenCmd{Link: strings.TrimSpace(arguments.Link)}
		return command.Run, true
	case "trawl_around":
		command := &AroundCmd{Target: strings.TrimSpace(arguments.Target), Trip: strings.TrimSpace(arguments.Trip), Window: arguments.Window, Trawler: arguments.Trawler, Limit: limitOrDefault(30)}
		return command.Run, true
	case "trawl_status":
		command := &StatusCmd{Trawler: strings.TrimSpace(arguments.Trawler)}
		return command.Run, true
	case "trawl_who":
		command := &WhoCmd{Name: strings.Fields(arguments.Name), Limit: limitOrDefault(20)}
		return command.Run, true
	default:
		return nil, false
	}
}

func (c *McpCmd) Run(r *Runtime) error {
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

func (c *McpCmd) runHTTP(r *Runtime) error {
	service := newAgentArchiveService(r.ctx, r.stateRoot)
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return render.AgentAccessOperationFailed("Cannot listen on " + c.Listen + ": " + err.Error())
	}
	defer func() { _ = listener.Close() }()

	listenIP, port, _ := net.SplitHostPort(listener.Addr().String())
	advertisedHost := listenIP
	if parsedIP := net.ParseIP(listenIP); parsedIP == nil || !parsedIP.IsLoopback() {
		if service.tailscaleMagicDNSHostName != "" {
			advertisedHost = service.tailscaleMagicDNSHostName
		} else if hostName, hostErr := os.Hostname(); hostErr == nil && hostName != "" {
			advertisedHost = hostName
		}
	}
	mcpEndpointURL := "http://" + net.JoinHostPort(advertisedHost, port) + "/mcp"

	sourceNames := make([]string, 0, len(service.installedTrawlersDiscoveredAtServerStart))
	for _, trawler := range service.installedTrawlersDiscoveredAtServerStart {
		if trawler.TrawlerDiscoveryError == nil {
			sourceNames = append(sourceNames, installedTrawlerIdentityText(trawler))
		}
	}
	facts := []render.FieldValueRow{
		{FieldLabel: "listening", FieldValue: "http://" + listener.Addr().String()},
		{FieldLabel: "MCP", FieldValue: mcpEndpointURL},
		{FieldLabel: "sources", FieldValue: strings.Join(sourceNames, ", ")},
	}
	if err := r.output.WriteAgentMCPServerListening(render.AgentMCPServerListeningPresentation{
		AgentMCPServerListeningSentence: "OpenTrawl MCP server is listening over HTTP. Every request needs a scoped bearer token.",
		AgentMCPServerFacts:             facts,
		NextTrawlCommands:               [][]string{{"connect", "--transport", "http"}},
	}); err != nil {
		return err
	}

	httpServer := &http.Server{
		Handler:           &agentMCPHTTPHandler{service: service},
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return render.AgentAccessOperationFailed("MCP HTTP server stopped: " + err.Error())
	}
	return nil
}

type agentMCPHTTPHandler struct {
	service *agentArchiveService
}

func (handler *agentMCPHTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/healthz" {
		writeMCPJSONBytes(response, http.StatusOK, mustMarshalMCPJSON(mcpHealthzResponse{Status: "ok"}))
		return
	}
	if request.URL.Path != "/mcp" {
		writeMCPHTTPError(response, http.StatusNotFound, "no such endpoint; use POST /mcp")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeMCPHTTPError(response, http.StatusMethodNotAllowed, "MCP over HTTP takes POST; this server does not open server-sent event streams")
		return
	}
	bearerToken, hasBearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !hasBearer {
		writeMCPHTTPError(response, http.StatusUnauthorized, "send Authorization: Bearer <token>; create one with trawl token create")
		return
	}
	token, err := findActiveAgentAccessToken(handler.service.stateRoot, bearerToken)
	if err != nil {
		writeMCPHTTPError(response, http.StatusUnauthorized, err.Error())
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, mcpMaximumHTTPBodyByteCount))
	if err != nil {
		writeMCPHTTPError(response, http.StatusBadRequest, err.Error())
		return
	}
	scope := token.agentSourceScope()
	mcpResponse := handler.service.handleMCPMessage(request.Context(), func() (agentSourceScope, error) {
		return scope, nil
	}, body)
	if mcpResponse == nil {
		response.WriteHeader(http.StatusAccepted)
		return
	}
	writeMCPJSONBytes(response, http.StatusOK, mustMarshalMCPJSON(mcpResponse))
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
