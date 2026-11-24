package cli

import (
	"encoding/json"
	"net/http"
)

const (
	remoteMCPServerCardWellKnownPath = "/.well-known/mcp/server-card.json"
	remoteMCPServerCardEndpointPath  = "/mcp/server-card"
	remoteMCPServerCardSchemaURI     = "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json"
	remoteMCPServerCardName          = "sh.opentrawl/opentrawl"
	remoteMCPServerCardTitle         = "OpenTrawl"
	remoteMCPServerCardDescription   = "Read-only personal archive MCP server for your messages, email, notes, calendar, and photos."
	remoteMCPServerCardWebsiteURL    = "https://opentrawl.sh"
	remoteMCPServerCardPrivacyURL    = "https://opentrawl.sh/privacy"
	remoteMCPServerCardRepositoryURL = "https://github.com/opentrawl/opentrawl"
)

type remoteMCPServerCardRepository struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

type remoteMCPServerCardIcon struct {
	Src      string   `json:"src"`
	MIMEType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
}

type remoteMCPServerCardRemote struct {
	Type                      string   `json:"type"`
	URL                       string   `json:"url"`
	SupportedProtocolVersions []string `json:"supportedProtocolVersions,omitempty"`
}

type remoteMCPServerCardAuthentication struct {
	Type                string   `json:"type"`
	AuthorizationServer string   `json:"authorization_server"`
	ProtectedResource   string   `json:"protected_resource"`
	ScopesSupported     []string `json:"scopes_supported"`
}

type remoteMCPServerCardMetadata struct {
	PrivacyURL string `json:"sh.opentrawl/privacyUrl"`
	Deployment string `json:"sh.opentrawl/deployment"`
}

// remoteMCPServerCardDocument conforms to MCP SEP-2127 (Server Cards v1) and
// also exposes the top-level url and authentication fields expected by
// integrations.sh discovery. It contains only static product metadata and the
// request's own endpoint origin, never personal archive data or host details.
type remoteMCPServerCardDocument struct {
	Schema         string                            `json:"$schema"`
	Name           string                            `json:"name"`
	Title          string                            `json:"title"`
	Version        string                            `json:"version"`
	Description    string                            `json:"description"`
	WebsiteURL     string                            `json:"websiteUrl"`
	Repository     remoteMCPServerCardRepository     `json:"repository"`
	Icons          []remoteMCPServerCardIcon         `json:"icons"`
	Remotes        []remoteMCPServerCardRemote       `json:"remotes"`
	URL            string                            `json:"url"`
	Authentication remoteMCPServerCardAuthentication `json:"authentication"`
	Meta           remoteMCPServerCardMetadata       `json:"_meta"`
}

func isRemoteMCPServerCardPath(path string) bool {
	return path == remoteMCPServerCardWellKnownPath || path == remoteMCPServerCardEndpointPath
}

func (handler *agentMCPHTTPHandler) handleRemoteMCPServerCard(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeMCPHTTPError(response, http.StatusMethodNotAllowed, "server card endpoint requires GET")
		return
	}
	origin := handler.publicOriginForServerCard(request)
	mcpURL := origin + "/mcp"
	document := remoteMCPServerCardDocument{
		Schema:      remoteMCPServerCardSchemaURI,
		Name:        remoteMCPServerCardName,
		Title:       remoteMCPServerCardTitle,
		Version:     Version,
		Description: remoteMCPServerCardDescription,
		WebsiteURL:  remoteMCPServerCardWebsiteURL,
		Repository: remoteMCPServerCardRepository{
			URL:    remoteMCPServerCardRepositoryURL,
			Source: "github",
		},
		Icons: []remoteMCPServerCardIcon{
			{
				Src:      "https://opentrawl.sh/apple-touch-icon.png",
				MIMEType: "image/png",
				Sizes:    []string{"180x180"},
			},
			{
				Src:      "https://opentrawl.sh/favicon-48.png",
				MIMEType: "image/png",
				Sizes:    []string{"48x48"},
			},
		},
		Remotes: []remoteMCPServerCardRemote{
			{
				Type:                      "streamable-http",
				URL:                       mcpURL,
				SupportedProtocolVersions: []string{mcpDefaultProtocolVersion, "2026-07-28"},
			},
		},
		URL: mcpURL,
		Authentication: remoteMCPServerCardAuthentication{
			Type:                "oauth2",
			AuthorizationServer: origin,
			ProtectedResource:   origin + remoteMCPOAuthProtectedResourceMCPPath,
			ScopesSupported:     []string{remoteMCPOAuthReadScope},
		},
		Meta: remoteMCPServerCardMetadata{
			PrivacyURL: remoteMCPServerCardPrivacyURL,
			Deployment: "personal-local-first",
		},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		writeMCPHTTPError(response, http.StatusInternalServerError, "server card could not be encoded")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeMCPJSONBytes(response, http.StatusOK, append(encoded, '\n'))
}

func (handler *agentMCPHTTPHandler) publicOriginForServerCard(request *http.Request) string {
	if handler.oauth != nil {
		return handler.oauth.publicOriginForRequest(request)
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + request.Host
}
