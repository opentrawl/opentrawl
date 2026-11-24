package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	remoteMCPOAuthProtectedResourcePath     = "/.well-known/oauth-protected-resource"
	remoteMCPOAuthProtectedResourceMCPPath  = "/.well-known/oauth-protected-resource/mcp"
	remoteMCPOAuthAuthorizationServerPath   = "/.well-known/oauth-authorization-server"
	remoteMCPOAuthOpenIDConfigurationPath   = "/.well-known/openid-configuration"
	remoteMCPOAuthRegisterPath              = "/oauth/register"
	remoteMCPOAuthAuthorizePath             = "/oauth/authorize"
	remoteMCPOAuthTokenPath                 = "/oauth/token"
	remoteMCPOAuthReadScope                 = "opentrawl:read"
	remoteMCPOAuthClientsFileName           = "oauth-clients.json"
	remoteMCPOAuthRefreshTokensFileName     = "oauth-refresh-tokens.json"
	remoteMCPOAuthRefreshTokenPrefix        = "otr_"
	remoteMCPOAuthAccessTokenLifetime       = time.Hour
	remoteMCPOAuthRefreshTokenLifetime      = 30 * 24 * time.Hour
	remoteMCPOAuthAuthorizationCodeLifetime = 60 * time.Second
	remoteMCPOAuthPendingConsentLifetime    = 5 * time.Minute
	remoteMCPOAuthDefaultPairingDuration    = 5 * time.Minute
	remoteMCPOAuthMaxPairingCodeAttempts    = 5
	remoteMCPOAuthMaxCIMDResponseBytes      = 64 << 10
	remoteMCPOAuthMaxRegisterBodyBytes      = 64 << 10
	remoteMCPOAuthMaxTokenFormBodyBytes     = 64 << 10
)

type remoteMCPOAuthClientRecord struct {
	ClientIdentifier string    `json:"client_id"`
	ClientName       string    `json:"client_name"`
	RedirectURIs     []string  `json:"redirect_uris"`
	CreatedTime      time.Time `json:"created_time"`
}

type remoteMCPOAuthClientStoreFile struct {
	RegisteredClients []remoteMCPOAuthClientRecord `json:"registered_clients"`
}

type remoteMCPOAuthRefreshTokenState string

const (
	remoteMCPOAuthRefreshTokenStateActive  remoteMCPOAuthRefreshTokenState = "active"
	remoteMCPOAuthRefreshTokenStateRotated remoteMCPOAuthRefreshTokenState = "rotated"
	remoteMCPOAuthRefreshTokenStateRevoked remoteMCPOAuthRefreshTokenState = "revoked"
)

type remoteMCPOAuthRefreshTokenRecord struct {
	RefreshTokenSHA256         string                          `json:"refresh_token_sha256"`
	AgentAccessTokenIdentifier AgentAccessTokenIdentifier      `json:"token_id"`
	ClientIdentifier           string                          `json:"client_id"`
	ResourceURI                string                          `json:"resource"`
	State                      remoteMCPOAuthRefreshTokenState `json:"state"`
	CreatedTime                time.Time                       `json:"created_time"`
	ExpiresTime                time.Time                       `json:"expires_time"`
}

type remoteMCPOAuthRefreshTokenStoreFile struct {
	RefreshTokens []remoteMCPOAuthRefreshTokenRecord `json:"refresh_tokens"`
}

type remoteMCPOAuthPendingConsent struct {
	CSRFToken        string
	ClientIdentifier string
	ClientName       string
	RedirectURI      string
	RedirectHost     string
	State            string
	CodeChallenge    string
	ResourceURI      string
	ExpiresTime      time.Time
}

type remoteMCPOAuthAuthorizationCode struct {
	ClientIdentifier   string
	ClientName         string
	RedirectURI        string
	CodeChallenge      string
	ResourceURI        string
	AllowedSourcesText string
	ExpiresTime        time.Time
}

type remoteMCPOAuthPairingWindowState string

const (
	remoteMCPOAuthPairingWindowStateClosed    remoteMCPOAuthPairingWindowState = "closed"
	remoteMCPOAuthPairingWindowStateOpen      remoteMCPOAuthPairingWindowState = "open"
	remoteMCPOAuthPairingWindowStateConsumed  remoteMCPOAuthPairingWindowState = "consumed"
	remoteMCPOAuthPairingWindowStateLockedOut remoteMCPOAuthPairingWindowState = "locked_out"
	remoteMCPOAuthPairingWindowStateExpired   remoteMCPOAuthPairingWindowState = "expired"
)

type remoteMCPOAuthPairingWindow struct {
	OneTimeCode      string
	ExpiresTime      time.Time
	FailedGuessCount int
	State            remoteMCPOAuthPairingWindowState
}

func (window *remoteMCPOAuthPairingWindow) currentState(now time.Time) remoteMCPOAuthPairingWindowState {
	if window == nil {
		return remoteMCPOAuthPairingWindowStateClosed
	}
	if window.State == remoteMCPOAuthPairingWindowStateOpen && !now.Before(window.ExpiresTime) {
		window.State = remoteMCPOAuthPairingWindowStateExpired
	}
	return window.State
}

func (window *remoteMCPOAuthPairingWindow) verifyCode(enteredCode string, now time.Time) (bool, remoteMCPOAuthPairingWindowState) {
	if state := window.currentState(now); state != remoteMCPOAuthPairingWindowStateOpen {
		return false, state
	}
	expectedCode := normalizePairingCodeInput(window.OneTimeCode)
	if subtle.ConstantTimeCompare([]byte(enteredCode), []byte(expectedCode)) == 1 {
		return true, remoteMCPOAuthPairingWindowStateOpen
	}
	window.FailedGuessCount++
	if window.FailedGuessCount >= remoteMCPOAuthMaxPairingCodeAttempts {
		window.State = remoteMCPOAuthPairingWindowStateLockedOut
	}
	return false, window.State
}

func (window *remoteMCPOAuthPairingWindow) markConsumed() {
	if window != nil && window.State == remoteMCPOAuthPairingWindowStateOpen {
		window.State = remoteMCPOAuthPairingWindowStateConsumed
	}
}

type remoteMCPOAuthProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported"`
}

type remoteMCPOAuthAuthorizationServerMetadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	RegistrationEndpoint                       string   `json:"registration_endpoint"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                            []string `json:"scopes_supported"`
	AuthorizationResponseIssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
	ClientIDMetadataDocumentSupported          bool     `json:"client_id_metadata_document_supported"`
}

type remoteMCPOAuthRegistrationRequest struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type remoteMCPOAuthRegistrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type remoteMCPOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type remoteMCPOAuthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type remoteMCPOAuthConsentView struct {
	ClientName       string
	RedirectHost     string
	CSRFToken        string
	Sources          []string
	ErrorMessage     string
	PairingWindowOff bool
}

var remoteMCPOAuthConsentHTMLTemplate = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Connect to OpenTrawl</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; max-width: 460px; margin: 48px auto; padding: 0 20px; color: #141414; line-height: 1.5; }
  h1 { font-size: 20px; margin: 0 0 8px; }
  .card { border: 1px solid #d4d4d8; border-radius: 12px; padding: 20px; background: #fff; }
  .meta { color: #52525b; font-size: 14px; margin: 0 0 16px; }
  .error { background: #fef2f2; color: #991b1b; border: 1px solid #fecaca; border-radius: 8px; padding: 10px 12px; font-size: 14px; margin-bottom: 16px; }
  label.field { display: block; font-weight: 600; font-size: 14px; margin: 14px 0 6px; }
  input[type=text] { width: 100%; box-sizing: border-box; font-size: 18px; letter-spacing: 0.12em; text-transform: uppercase; padding: 10px 12px; border: 1px solid #a1a1aa; border-radius: 8px; font-family: ui-monospace, monospace; }
  .sources { display: grid; grid-template-columns: 1fr 1fr; gap: 6px 12px; margin: 8px 0 18px; }
  .sources label { font-size: 14px; display: flex; align-items: center; gap: 6px; }
  button { width: 100%; background: #18181b; color: #fff; border: 0; border-radius: 8px; padding: 11px 16px; font-size: 15px; font-weight: 600; cursor: pointer; }
  .deny { background: transparent; color: #52525b; border: 1px solid #d4d4d8; margin-top: 8px; }
</style>
</head>
<body>
<div class="card">
{{if .PairingWindowOff}}
  <h1>Pairing is closed</h1>
  <p class="meta">OpenTrawl on this Mac is not currently accepting new cloud agent pairings. Open a 5-minute pairing window on your Mac (for example, run <code>trawl mcp --relay --pair</code>) and try again.</p>
{{else}}
  <h1>Connect {{.ClientName}}</h1>
  <p class="meta">Read-only access to your OpenTrawl archive. Redirects to <strong>{{.RedirectHost}}</strong>.</p>
  {{if .ErrorMessage}}<div class="error">{{.ErrorMessage}}</div>{{end}}
  <form method="POST" action="/oauth/authorize">
    <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
    <label class="field" for="pairing_code">One-time code shown on your Mac</label>
    <input id="pairing_code" name="pairing_code" type="text" autocomplete="off" placeholder="ABCD-1234" required>
    <label class="field">Allowed sources</label>
    <div class="sources">
      {{range .Sources}}
      <label><input type="checkbox" name="source" value="{{.}}" checked> {{.}}</label>
      {{end}}
    </div>
    <button type="submit" name="decision" value="approve">Approve Read-Only Access</button>
    <button type="submit" name="decision" value="deny" class="deny">Cancel</button>
  </form>
{{end}}
</div>
</body>
</html>
`))

type remoteMCPOAuthServer struct {
	stateRoot         string
	fixedPublicOrigin string
	availableSources  []string
	now               func() time.Time
	cimdHTTPClient    *http.Client

	mu                 sync.Mutex
	pairing            *remoteMCPOAuthPairingWindow
	pendingConsents    map[string]remoteMCPOAuthPendingConsent
	authorizationCodes map[string]remoteMCPOAuthAuthorizationCode
}

func newRemoteMCPOAuthServer(stateRoot, fixedPublicOrigin string, availableSources []string) *remoteMCPOAuthServer {
	sortedSources := slices.Clone(availableSources)
	slices.Sort(sortedSources)
	return &remoteMCPOAuthServer{
		stateRoot:          stateRoot,
		fixedPublicOrigin:  strings.TrimRight(strings.TrimSpace(fixedPublicOrigin), "/"),
		availableSources:   sortedSources,
		now:                time.Now,
		cimdHTTPClient:     newSSRFProtectedCIMDHTTPClient(),
		pendingConsents:    map[string]remoteMCPOAuthPendingConsent{},
		authorizationCodes: map[string]remoteMCPOAuthAuthorizationCode{},
	}
}

// OpenPairingWindow opens a single-use pairing window and returns the human-
// readable one-time code shown only on the Mac (e.g. "K7M2-P9Q4").
func (server *remoteMCPOAuthServer) OpenPairingWindow(duration time.Duration) (string, time.Time, error) {
	if duration <= 0 {
		duration = remoteMCPOAuthDefaultPairingDuration
	}
	code, err := generateHumanReadablePairingCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := server.now().UTC().Add(duration)
	server.mu.Lock()
	server.pairing = &remoteMCPOAuthPairingWindow{
		OneTimeCode: code,
		ExpiresTime: expiresAt,
		State:       remoteMCPOAuthPairingWindowStateOpen,
	}
	server.mu.Unlock()
	return code, expiresAt, nil
}

func generateHumanReadablePairingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var out [9]byte
	for i := 0; i < 4; i++ {
		out[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	out[4] = '-'
	for i := 4; i < 8; i++ {
		out[i+1] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(out[:]), nil
}

func (server *remoteMCPOAuthServer) publicOriginForRequest(request *http.Request) string {
	if server.fixedPublicOrigin != "" {
		return server.fixedPublicOrigin
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + request.Host
}

func (server *remoteMCPOAuthServer) protectedResourceMetadataURL(request *http.Request) string {
	return server.publicOriginForRequest(request) + remoteMCPOAuthProtectedResourceMCPPath
}

func (server *remoteMCPOAuthServer) isOAuthPath(path string) bool {
	switch path {
	case remoteMCPOAuthProtectedResourcePath,
		remoteMCPOAuthProtectedResourceMCPPath,
		remoteMCPOAuthAuthorizationServerPath,
		remoteMCPOAuthOpenIDConfigurationPath,
		remoteMCPOAuthRegisterPath,
		remoteMCPOAuthAuthorizePath,
		remoteMCPOAuthTokenPath:
		return true
	default:
		return false
	}
}

func (server *remoteMCPOAuthServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case remoteMCPOAuthProtectedResourcePath, remoteMCPOAuthProtectedResourceMCPPath:
		server.handleProtectedResourceMetadata(response, request)
	case remoteMCPOAuthAuthorizationServerPath, remoteMCPOAuthOpenIDConfigurationPath:
		server.handleAuthorizationServerMetadata(response, request)
	case remoteMCPOAuthRegisterPath:
		server.handleClientRegistration(response, request)
	case remoteMCPOAuthAuthorizePath:
		server.handleAuthorize(response, request)
	case remoteMCPOAuthTokenPath:
		server.handleToken(response, request)
	default:
		writeOAuthJSONError(response, http.StatusNotFound, "invalid_request", "unknown OAuth endpoint")
	}
}

func (server *remoteMCPOAuthServer) handleProtectedResourceMetadata(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeOAuthJSONError(response, http.StatusMethodNotAllowed, "invalid_request", "metadata endpoint requires GET")
		return
	}
	origin := server.publicOriginForRequest(request)
	writeOAuthJSON(response, http.StatusOK, remoteMCPOAuthProtectedResourceMetadata{
		Resource:               origin + "/mcp",
		AuthorizationServers:   []string{origin},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        []string{remoteMCPOAuthReadScope},
	})
}

func (server *remoteMCPOAuthServer) handleAuthorizationServerMetadata(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeOAuthJSONError(response, http.StatusMethodNotAllowed, "invalid_request", "metadata endpoint requires GET")
		return
	}
	origin := server.publicOriginForRequest(request)
	writeOAuthJSON(response, http.StatusOK, remoteMCPOAuthAuthorizationServerMetadata{
		Issuer:                                     origin,
		AuthorizationEndpoint:                      origin + remoteMCPOAuthAuthorizePath,
		TokenEndpoint:                              origin + remoteMCPOAuthTokenPath,
		RegistrationEndpoint:                       origin + remoteMCPOAuthRegisterPath,
		ResponseTypesSupported:                     []string{"code"},
		GrantTypesSupported:                        []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:              []string{"S256"},
		TokenEndpointAuthMethodsSupported:          []string{"none", "client_secret_post"},
		ScopesSupported:                            []string{remoteMCPOAuthReadScope},
		AuthorizationResponseIssParameterSupported: true,
		ClientIDMetadataDocumentSupported:          true,
	})
}

func (server *remoteMCPOAuthServer) handleClientRegistration(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeOAuthJSONError(response, http.StatusMethodNotAllowed, "invalid_request", "registration requires POST")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, remoteMCPOAuthMaxRegisterBodyBytes))
	if err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_client_metadata", "could not read registration body")
		return
	}
	var registration remoteMCPOAuthRegistrationRequest
	if err := json.Unmarshal(body, &registration); err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_client_metadata", "registration body must be JSON")
		return
	}
	if len(registration.RedirectURIs) == 0 {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris must contain at least one URI")
		return
	}
	cleanedURIs := make([]string, 0, len(registration.RedirectURIs))
	for _, rawURI := range registration.RedirectURIs {
		if err := validateOAuthRedirectURI(rawURI); err != nil {
			writeOAuthJSONError(response, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
		cleanedURIs = append(cleanedURIs, strings.TrimSpace(rawURI))
	}
	clientName := strings.TrimSpace(registration.ClientName)
	if clientName == "" {
		parsed, _ := url.Parse(cleanedURIs[0])
		if parsed != nil && parsed.Host != "" {
			clientName = parsed.Host
		} else {
			clientName = "MCP Client"
		}
	}
	clientIDBytes := make([]byte, 12)
	if _, err := rand.Read(clientIDBytes); err != nil {
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not allocate client identifier")
		return
	}
	record := remoteMCPOAuthClientRecord{
		ClientIdentifier: "otc_" + hex.EncodeToString(clientIDBytes),
		ClientName:       clientName,
		RedirectURIs:     cleanedURIs,
		CreatedTime:      server.now().UTC(),
	}
	if err := server.saveRegisteredClient(record); err != nil {
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not store registered client")
		return
	}
	writeOAuthJSON(response, http.StatusCreated, remoteMCPOAuthRegistrationResponse{
		ClientID:                record.ClientIdentifier,
		ClientName:              record.ClientName,
		RedirectURIs:            record.RedirectURIs,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	})
}

func (server *remoteMCPOAuthServer) handleAuthorize(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.handleAuthorizeGet(response, request)
	case http.MethodPost:
		server.handleAuthorizePost(response, request)
	default:
		response.Header().Set("Allow", "GET, POST")
		writeOAuthJSONError(response, http.StatusMethodNotAllowed, "invalid_request", "authorize supports GET and POST")
	}
}

func (server *remoteMCPOAuthServer) handleAuthorizeGet(response http.ResponseWriter, request *http.Request) {
	if !server.isPairingWindowActive() {
		renderConsentHTML(response, http.StatusOK, remoteMCPOAuthConsentView{PairingWindowOff: true})
		return
	}
	query := request.URL.Query()
	clientID := strings.TrimSpace(query.Get("client_id"))
	redirectURI := strings.TrimSpace(query.Get("redirect_uri"))
	responseType := strings.TrimSpace(query.Get("response_type"))
	codeChallenge := strings.TrimSpace(query.Get("code_challenge"))
	codeChallengeMethod := strings.TrimSpace(query.Get("code_challenge_method"))
	state := query.Get("state")
	resourceURI := strings.TrimSpace(query.Get("resource"))

	clientRecord, err := server.resolveOAuthClient(request.Context(), clientID)
	if err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_client", err.Error())
		return
	}
	if !slices.Contains(clientRecord.RedirectURIs, redirectURI) {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri is not registered for this client")
		return
	}
	if responseType != "code" {
		redirectOAuthError(response, request, redirectURI, state, server.publicOriginForRequest(request), "unsupported_response_type", "response_type must be code")
		return
	}
	if codeChallenge == "" || codeChallengeMethod != "S256" {
		redirectOAuthError(response, request, redirectURI, state, server.publicOriginForRequest(request), "invalid_request", "PKCE S256 code_challenge is required")
		return
	}
	expectedResource := server.publicOriginForRequest(request) + "/mcp"
	if resourceURI != "" && resourceURI != expectedResource {
		redirectOAuthError(response, request, redirectURI, state, server.publicOriginForRequest(request), "invalid_target", "resource must match "+expectedResource)
		return
	}
	if resourceURI == "" {
		resourceURI = expectedResource
	}

	csrfBytes := make([]byte, 24)
	if _, err := rand.Read(csrfBytes); err != nil {
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not start consent flow")
		return
	}
	csrfToken := base64.RawURLEncoding.EncodeToString(csrfBytes)
	parsedRedirect, _ := url.Parse(redirectURI)
	redirectHost := redirectURI
	if parsedRedirect != nil && parsedRedirect.Host != "" {
		redirectHost = parsedRedirect.Host
	}

	now := server.now().UTC()
	server.mu.Lock()
	server.pruneExpiredEphemeralStateLocked(now)
	server.pendingConsents[csrfToken] = remoteMCPOAuthPendingConsent{
		CSRFToken:        csrfToken,
		ClientIdentifier: clientRecord.ClientIdentifier,
		ClientName:       clientRecord.ClientName,
		RedirectURI:      redirectURI,
		RedirectHost:     redirectHost,
		State:            state,
		CodeChallenge:    codeChallenge,
		ResourceURI:      resourceURI,
		ExpiresTime:      now.Add(remoteMCPOAuthPendingConsentLifetime),
	}
	server.mu.Unlock()

	renderConsentHTML(response, http.StatusOK, remoteMCPOAuthConsentView{
		ClientName:   clientRecord.ClientName,
		RedirectHost: redirectHost,
		CSRFToken:    csrfToken,
		Sources:      server.availableSources,
	})
}

func (server *remoteMCPOAuthServer) handleAuthorizePost(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, remoteMCPOAuthMaxTokenFormBodyBytes)
	if err := request.ParseForm(); err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_request", "could not parse consent form")
		return
	}
	csrfToken := strings.TrimSpace(request.FormValue("csrf_token"))
	decision := strings.TrimSpace(request.FormValue("decision"))
	enteredCode := normalizePairingCodeInput(request.FormValue("pairing_code"))
	selectedSources := request.Form["source"]

	now := server.now().UTC()
	server.mu.Lock()
	server.pruneExpiredEphemeralStateLocked(now)
	pending, exists := server.pendingConsents[csrfToken]
	if !exists || now.After(pending.ExpiresTime) {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_request", "consent session expired; restart sign-in from your agent")
		return
	}
	if decision != "approve" {
		delete(server.pendingConsents, csrfToken)
		server.mu.Unlock()
		redirectOAuthError(response, request, pending.RedirectURI, pending.State, server.publicOriginForRequest(request), "access_denied", "user denied access")
		return
	}
	if server.pairing.currentState(now) != remoteMCPOAuthPairingWindowStateOpen {
		delete(server.pendingConsents, csrfToken)
		server.mu.Unlock()
		renderConsentHTML(response, http.StatusOK, remoteMCPOAuthConsentView{PairingWindowOff: true})
		return
	}
	matched, nextState := server.pairing.verifyCode(enteredCode, now)
	if !matched {
		windowClosed := nextState != remoteMCPOAuthPairingWindowStateOpen
		if windowClosed {
			delete(server.pendingConsents, csrfToken)
		}
		server.mu.Unlock()
		if windowClosed {
			renderConsentHTML(response, http.StatusOK, remoteMCPOAuthConsentView{PairingWindowOff: true})
			return
		}
		renderConsentHTML(response, http.StatusOK, remoteMCPOAuthConsentView{
			ClientName:   pending.ClientName,
			RedirectHost: pending.RedirectHost,
			CSRFToken:    pending.CSRFToken,
			Sources:      server.availableSources,
			ErrorMessage: "Pairing code does not match the code shown on your Mac.",
		})
		return
	}

	allowedSources := make([]string, 0, len(selectedSources))
	for _, candidate := range selectedSources {
		if slices.Contains(server.availableSources, candidate) && !slices.Contains(allowedSources, candidate) {
			allowedSources = append(allowedSources, candidate)
		}
	}
	if len(allowedSources) == 0 {
		server.mu.Unlock()
		renderConsentHTML(response, http.StatusOK, remoteMCPOAuthConsentView{
			ClientName:   pending.ClientName,
			RedirectHost: pending.RedirectHost,
			CSRFToken:    pending.CSRFToken,
			Sources:      server.availableSources,
			ErrorMessage: "Select at least one source to share.",
		})
		return
	}

	// Valid one-time code and source selection: consume both pairing window and CSRF consent session.
	server.pairing.markConsumed()
	delete(server.pendingConsents, csrfToken)

	allowedSourcesText := agentAccessAllSourcesKeyword
	if len(allowedSources) < len(server.availableSources) {
		allowedSourcesText = strings.Join(allowedSources, ",")
	}
	authCodeBytes := make([]byte, 32)
	if _, err := rand.Read(authCodeBytes); err != nil {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not mint authorization code")
		return
	}
	authCode := base64.RawURLEncoding.EncodeToString(authCodeBytes)
	server.authorizationCodes[authCode] = remoteMCPOAuthAuthorizationCode{
		ClientIdentifier:   pending.ClientIdentifier,
		ClientName:         pending.ClientName,
		RedirectURI:        pending.RedirectURI,
		CodeChallenge:      pending.CodeChallenge,
		ResourceURI:        pending.ResourceURI,
		AllowedSourcesText: allowedSourcesText,
		ExpiresTime:        now.Add(remoteMCPOAuthAuthorizationCodeLifetime),
	}
	server.mu.Unlock()

	targetURL, err := url.Parse(pending.RedirectURI)
	if err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_redirect_uri", "invalid redirect_uri")
		return
	}
	params := targetURL.Query()
	params.Set("code", authCode)
	if pending.State != "" {
		params.Set("state", pending.State)
	}
	params.Set("iss", server.publicOriginForRequest(request))
	targetURL.RawQuery = params.Encode()
	http.Redirect(response, request, targetURL.String(), http.StatusFound)
}

func (server *remoteMCPOAuthServer) handleToken(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeOAuthJSONError(response, http.StatusMethodNotAllowed, "invalid_request", "token endpoint requires POST")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, remoteMCPOAuthMaxTokenFormBodyBytes)
	if err := request.ParseForm(); err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_request", "could not parse token form")
		return
	}
	grantType := strings.TrimSpace(request.FormValue("grant_type"))
	switch grantType {
	case "authorization_code":
		server.handleAuthorizationCodeGrant(response, request)
	case "refresh_token":
		server.handleRefreshTokenGrant(response, request)
	default:
		writeOAuthJSONError(response, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code and refresh_token are supported")
	}
}

func (server *remoteMCPOAuthServer) handleAuthorizationCodeGrant(response http.ResponseWriter, request *http.Request) {
	code := strings.TrimSpace(request.FormValue("code"))
	redirectURI := strings.TrimSpace(request.FormValue("redirect_uri"))
	clientID := strings.TrimSpace(request.FormValue("client_id"))
	codeVerifier := strings.TrimSpace(request.FormValue("code_verifier"))
	resourceURI := strings.TrimSpace(request.FormValue("resource"))

	now := server.now().UTC()
	server.mu.Lock()
	server.pruneExpiredEphemeralStateLocked(now)
	grant, found := server.authorizationCodes[code]
	if found {
		delete(server.authorizationCodes, code)
	}
	server.mu.Unlock()

	if !found || now.After(grant.ExpiresTime) {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if clientID != grant.ClientIdentifier {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "client_id does not match authorization code")
		return
	}
	if redirectURI != grant.RedirectURI {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match authorization code")
		return
	}
	if resourceURI != "" && resourceURI != grant.ResourceURI {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_target", "resource does not match authorization code")
		return
	}
	if !verifyPKCES256(codeVerifier, grant.CodeChallenge) {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "PKCE code_verifier verification failed")
		return
	}

	accessExpiry := now.Add(remoteMCPOAuthAccessTokenLifetime)
	tokenRecord, accessToken, err := createScopedAgentAccessToken(
		server.stateRoot,
		grant.ClientName,
		grant.AllowedSourcesText,
		now,
		&accessExpiry,
	)
	if err != nil {
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not issue access token")
		return
	}
	refreshToken, err := server.issueRefreshToken(tokenRecord.AgentAccessTokenIdentifier, grant.ClientIdentifier, grant.ResourceURI, now)
	if err != nil {
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not issue refresh token")
		return
	}
	writeOAuthJSON(response, http.StatusOK, remoteMCPOAuthTokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(remoteMCPOAuthAccessTokenLifetime / time.Second),
		RefreshToken: refreshToken,
		Scope:        remoteMCPOAuthReadScope,
	})
}

func (server *remoteMCPOAuthServer) handleRefreshTokenGrant(response http.ResponseWriter, request *http.Request) {
	refreshToken := strings.TrimSpace(request.FormValue("refresh_token"))
	clientID := strings.TrimSpace(request.FormValue("client_id"))
	resourceURI := strings.TrimSpace(request.FormValue("resource"))
	if !strings.HasPrefix(refreshToken, remoteMCPOAuthRefreshTokenPrefix) {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "refresh token is invalid")
		return
	}

	now := server.now().UTC()
	wantedHash := bearerTokenSHA256Hex(refreshToken)

	server.mu.Lock()
	records, err := server.loadRefreshTokensLocked()
	if err != nil {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not read refresh token store")
		return
	}
	matchIndex := -1
	for index, record := range records {
		if subtle.ConstantTimeCompare([]byte(record.RefreshTokenSHA256), []byte(wantedHash)) == 1 {
			matchIndex = index
			break
		}
	}
	if matchIndex < 0 {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "refresh token is not recognised")
		return
	}
	matched := records[matchIndex]
	switch matched.State {
	case remoteMCPOAuthRefreshTokenStateRotated:
		// Reuse of an already-rotated refresh token: revoke the underlying grant.
		records[matchIndex].State = remoteMCPOAuthRefreshTokenStateRevoked
		_ = server.saveRefreshTokensLocked(records)
		server.mu.Unlock()
		_, _ = revokeAgentAccessToken(server.stateRoot, matched.AgentAccessTokenIdentifier, now)
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "refresh token was already used; grant revoked")
		return
	case remoteMCPOAuthRefreshTokenStateRevoked:
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "refresh token was revoked")
		return
	case remoteMCPOAuthRefreshTokenStateActive:
		// Active token; continue validating expiry, binding, and grant liveness.
	default:
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "refresh token state is invalid")
		return
	}
	if now.After(matched.ExpiresTime) {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "refresh token has expired")
		return
	}
	if clientID != "" && clientID != matched.ClientIdentifier {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "client_id does not match refresh token")
		return
	}
	if resourceURI != "" && resourceURI != matched.ResourceURI {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_target", "resource does not match refresh token")
		return
	}
	active, err := isAgentAccessTokenIdentifierActive(server.stateRoot, matched.AgentAccessTokenIdentifier)
	if err != nil || !active {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "grant was revoked")
		return
	}

	records[matchIndex].State = remoteMCPOAuthRefreshTokenStateRotated
	newRefreshSecret := make([]byte, 32)
	if _, err := rand.Read(newRefreshSecret); err != nil {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not rotate refresh token")
		return
	}
	newRefreshToken := remoteMCPOAuthRefreshTokenPrefix + base64.RawURLEncoding.EncodeToString(newRefreshSecret)
	records = append(records, remoteMCPOAuthRefreshTokenRecord{
		RefreshTokenSHA256:         bearerTokenSHA256Hex(newRefreshToken),
		AgentAccessTokenIdentifier: matched.AgentAccessTokenIdentifier,
		ClientIdentifier:           matched.ClientIdentifier,
		ResourceURI:                matched.ResourceURI,
		State:                      remoteMCPOAuthRefreshTokenStateActive,
		CreatedTime:                now,
		ExpiresTime:                now.Add(remoteMCPOAuthRefreshTokenLifetime),
	})
	if err := server.saveRefreshTokensLocked(records); err != nil {
		server.mu.Unlock()
		writeOAuthJSONError(response, http.StatusInternalServerError, "server_error", "could not save rotated refresh token")
		return
	}
	server.mu.Unlock()

	accessExpiry := now.Add(remoteMCPOAuthAccessTokenLifetime)
	_, newAccessToken, err := replaceActiveAgentAccessTokenSecret(server.stateRoot, matched.AgentAccessTokenIdentifier, now, accessExpiry)
	if err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, "invalid_grant", "grant is no longer active")
		return
	}
	writeOAuthJSON(response, http.StatusOK, remoteMCPOAuthTokenResponse{
		AccessToken:  newAccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(remoteMCPOAuthAccessTokenLifetime / time.Second),
		RefreshToken: newRefreshToken,
		Scope:        remoteMCPOAuthReadScope,
	})
}

func (server *remoteMCPOAuthServer) isPairingWindowActive() bool {
	now := server.now().UTC()
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.pairing.currentState(now) == remoteMCPOAuthPairingWindowStateOpen
}

func (server *remoteMCPOAuthServer) pruneExpiredEphemeralStateLocked(now time.Time) {
	for token, pending := range server.pendingConsents {
		if now.After(pending.ExpiresTime) {
			delete(server.pendingConsents, token)
		}
	}
	for code, grant := range server.authorizationCodes {
		if now.After(grant.ExpiresTime) {
			delete(server.authorizationCodes, code)
		}
	}
}

func (server *remoteMCPOAuthServer) resolveOAuthClient(ctx context.Context, clientID string) (remoteMCPOAuthClientRecord, error) {
	if clientID == "" {
		return remoteMCPOAuthClientRecord{}, errors.New("client_id is required")
	}
	if strings.HasPrefix(clientID, "https://") {
		return server.fetchClientIDMetadataDocument(ctx, clientID)
	}
	clients, err := server.loadRegisteredClients()
	if err != nil {
		return remoteMCPOAuthClientRecord{}, err
	}
	for _, client := range clients {
		if client.ClientIdentifier == clientID {
			return client, nil
		}
	}
	return remoteMCPOAuthClientRecord{}, errors.New("unknown client_id; register via /oauth/register or use an HTTPS client_id metadata URL")
}

func (server *remoteMCPOAuthServer) fetchClientIDMetadataDocument(ctx context.Context, clientIDURL string) (remoteMCPOAuthClientRecord, error) {
	parsed, err := url.Parse(clientIDURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return remoteMCPOAuthClientRecord{}, errors.New("client_id metadata URL must be a valid https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientIDURL, nil)
	if err != nil {
		return remoteMCPOAuthClientRecord{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := server.cimdHTTPClient.Do(req)
	if err != nil {
		return remoteMCPOAuthClientRecord{}, errors.New("could not fetch client_id metadata document")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return remoteMCPOAuthClientRecord{}, errors.New("client_id metadata document returned non-200 status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, remoteMCPOAuthMaxCIMDResponseBytes))
	if err != nil {
		return remoteMCPOAuthClientRecord{}, err
	}
	var document struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return remoteMCPOAuthClientRecord{}, errors.New("client_id metadata document is not valid JSON")
	}
	if document.ClientID != clientIDURL {
		return remoteMCPOAuthClientRecord{}, errors.New("client_id metadata document client_id does not match URL")
	}
	if len(document.RedirectURIs) == 0 {
		return remoteMCPOAuthClientRecord{}, errors.New("client_id metadata document has no redirect_uris")
	}
	for _, uri := range document.RedirectURIs {
		if err := validateOAuthRedirectURI(uri); err != nil {
			return remoteMCPOAuthClientRecord{}, err
		}
	}
	name := strings.TrimSpace(document.ClientName)
	if name == "" {
		name = parsed.Host
	}
	return remoteMCPOAuthClientRecord{
		ClientIdentifier: document.ClientID,
		ClientName:       name,
		RedirectURIs:     document.RedirectURIs,
	}, nil
}

func newSSRFProtectedCIMDHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ipAddr := range ips {
				if isDisallowedCIMDIP(ipAddr.IP) {
					return nil, errors.New("client_id metadata host resolves to non-public address")
				}
			}
			if len(ips) == 0 {
				return nil, errors.New("client_id metadata host has no IP addresses")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func isDisallowedCIMDIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func validateOAuthRedirectURI(rawURI string) error {
	trimmed := strings.TrimSpace(rawURI)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.Fragment != "" {
		return errors.New("redirect_uri must be an absolute URL without fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return errors.New("redirect_uri must use https or loopback http")
}

func verifyPKCES256(codeVerifier, expectedChallenge string) bool {
	if len(codeVerifier) < 43 || len(codeVerifier) > 128 {
		return false
	}
	digest := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(digest[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(expectedChallenge)) == 1
}

func normalizePairingCodeInput(raw string) string {
	cleaned := strings.ToUpper(strings.TrimSpace(raw))
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	return cleaned
}

func (server *remoteMCPOAuthServer) clientsFilePath() string {
	return filepath.Join(server.stateRoot, agentAccessDirectoryName, remoteMCPOAuthClientsFileName)
}

func (server *remoteMCPOAuthServer) refreshTokensFilePath() string {
	return filepath.Join(server.stateRoot, agentAccessDirectoryName, remoteMCPOAuthRefreshTokensFileName)
}

func (server *remoteMCPOAuthServer) loadRegisteredClients() ([]remoteMCPOAuthClientRecord, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	encoded, err := os.ReadFile(server.clientsFilePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var store remoteMCPOAuthClientStoreFile
	if err := json.Unmarshal(encoded, &store); err != nil {
		return nil, err
	}
	return store.RegisteredClients, nil
}

func (server *remoteMCPOAuthServer) saveRegisteredClient(record remoteMCPOAuthClientRecord) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	var existing []remoteMCPOAuthClientRecord
	if encoded, err := os.ReadFile(server.clientsFilePath()); err == nil {
		var store remoteMCPOAuthClientStoreFile
		if json.Unmarshal(encoded, &store) == nil {
			existing = store.RegisteredClients
		}
	}
	existing = append(existing, record)
	encoded, err := json.MarshalIndent(remoteMCPOAuthClientStoreFile{RegisteredClients: existing}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateStateFileAtomically(server.clientsFilePath(), append(encoded, '\n'))
}

func (server *remoteMCPOAuthServer) issueRefreshToken(
	tokenIdentifier AgentAccessTokenIdentifier,
	clientID string,
	resourceURI string,
	now time.Time,
) (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	refreshToken := remoteMCPOAuthRefreshTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	server.mu.Lock()
	defer server.mu.Unlock()
	records, err := server.loadRefreshTokensLocked()
	if err != nil {
		return "", err
	}
	records = append(records, remoteMCPOAuthRefreshTokenRecord{
		RefreshTokenSHA256:         bearerTokenSHA256Hex(refreshToken),
		AgentAccessTokenIdentifier: tokenIdentifier,
		ClientIdentifier:           clientID,
		ResourceURI:                resourceURI,
		State:                      remoteMCPOAuthRefreshTokenStateActive,
		CreatedTime:                now,
		ExpiresTime:                now.Add(remoteMCPOAuthRefreshTokenLifetime),
	})
	if err := server.saveRefreshTokensLocked(records); err != nil {
		return "", err
	}
	return refreshToken, nil
}

func (server *remoteMCPOAuthServer) loadRefreshTokensLocked() ([]remoteMCPOAuthRefreshTokenRecord, error) {
	encoded, err := os.ReadFile(server.refreshTokensFilePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var store remoteMCPOAuthRefreshTokenStoreFile
	if err := json.Unmarshal(encoded, &store); err != nil {
		return nil, err
	}
	return store.RefreshTokens, nil
}

func (server *remoteMCPOAuthServer) saveRefreshTokensLocked(records []remoteMCPOAuthRefreshTokenRecord) error {
	encoded, err := json.MarshalIndent(remoteMCPOAuthRefreshTokenStoreFile{RefreshTokens: records}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateStateFileAtomically(server.refreshTokensFilePath(), append(encoded, '\n'))
}

func writePrivateStateFileAtomically(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
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

func renderConsentHTML(response http.ResponseWriter, statusCode int, view remoteMCPOAuthConsentView) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Frame-Options", "DENY")
	// Omit form-action: Chromium enforces form-action on the 302 redirect chain
	// following the consent form POST, which would block the cross-origin OAuth
	// callback to the client's redirect_uri.
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	response.WriteHeader(statusCode)
	_ = remoteMCPOAuthConsentHTMLTemplate.Execute(response, view)
}

func redirectOAuthError(response http.ResponseWriter, request *http.Request, redirectURI, state, issuer, errCode, errDesc string) {
	targetURL, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthJSONError(response, http.StatusBadRequest, errCode, errDesc)
		return
	}
	params := targetURL.Query()
	params.Set("error", errCode)
	if errDesc != "" {
		params.Set("error_description", errDesc)
	}
	if state != "" {
		params.Set("state", state)
	}
	params.Set("iss", issuer)
	targetURL.RawQuery = params.Encode()
	http.Redirect(response, request, targetURL.String(), http.StatusFound)
}

func writeOAuthJSON[T remoteMCPOAuthProtectedResourceMetadata | remoteMCPOAuthAuthorizationServerMetadata | remoteMCPOAuthRegistrationResponse | remoteMCPOAuthTokenResponse | remoteMCPOAuthErrorResponse](
	response http.ResponseWriter,
	statusCode int,
	payload T,
) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{"error":"server_error"}`)
		statusCode = http.StatusInternalServerError
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(statusCode)
	_, _ = response.Write(append(encoded, '\n'))
}

func writeOAuthJSONError(response http.ResponseWriter, statusCode int, errCode, errDesc string) {
	writeOAuthJSON(response, statusCode, remoteMCPOAuthErrorResponse{
		Error:            errCode,
		ErrorDescription: errDesc,
	})
}
