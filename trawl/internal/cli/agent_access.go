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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AgentAccessTokenIdentifier names a token in lists and revocation. It is not
// the secret bearer token.
type AgentAccessTokenIdentifier string

// agentAccessTokenSurface names which transport listener may accept a token.
type agentAccessTokenSurface string

const (
	agentAccessTokenSurfaceLocal agentAccessTokenSurface = "local"
	agentAccessTokenSurfaceRelay agentAccessTokenSurface = "relay"
)

// agentAccessTokenRecord is one stored token. Only the SHA-256 of the bearer
// token is kept, so the store cannot leak a usable token.
type agentAccessTokenRecord struct {
	AgentAccessTokenIdentifier AgentAccessTokenIdentifier `json:"token_id"`
	AgentName                  string                     `json:"agent_name"`
	BearerTokenSHA256          string                     `json:"bearer_token_sha256"`
	AllowedSurface             agentAccessTokenSurface    `json:"allowed_surface,omitempty"`
	AllSourcesAllowed          bool                       `json:"all_sources_allowed"`
	AllowedSources             []string                   `json:"allowed_sources"`
	CreatedTime                time.Time                  `json:"created_time"`
	ExpiresTime                *time.Time                 `json:"expires_time,omitempty"`
	RevokedTime                *time.Time                 `json:"revoked_time,omitempty"`
}

func (token agentAccessTokenRecord) allowsSurface(surface agentAccessTokenSurface) bool {
	return token.AllowedSurface != "" && token.AllowedSurface == surface
}

type agentAccessTokenFile struct {
	AgentAccessTokens []agentAccessTokenRecord `json:"agent_access_tokens"`
}

const (
	agentAccessDirectoryName       = "agent-access"
	agentAccessTokenFileName       = "access-tokens.json"
	agentAccessStoreLockFileName   = ".store.lock"
	agentAccessBearerTokenPrefix   = "otk_"
	agentAccessAllSourcesKeyword   = "all"
	agentAccessTokenEnvironmentKey = "OPENTRAWL_ACCESS_TOKEN"
)

var errAgentAccessTokenNotRecognised = errors.New("access token is not recognised or was revoked")

// agentAccessTokenStoreMutex serialises access-store mutations within one
// process; withAgentAccessStoreLock pairs it with syscall.Flock across processes.
var agentAccessTokenStoreMutex sync.Mutex

func withAgentAccessStoreLock(stateRoot string, operation func() error) error {
	agentAccessTokenStoreMutex.Lock()
	defer agentAccessTokenStoreMutex.Unlock()

	lockDir := filepath.Join(stateRoot, agentAccessDirectoryName)
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(lockDir, agentAccessStoreLockFileName)
	// #nosec G304 -- lockPath is derived solely from stateRoot and a fixed filename.
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lockFile.Close() }()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
	return operation()
}

func agentAccessTokenFilePath(stateRoot string) string {
	return filepath.Join(stateRoot, agentAccessDirectoryName, agentAccessTokenFileName)
}

func loadAgentAccessTokens(stateRoot string) ([]agentAccessTokenRecord, error) {
	encoded, err := os.ReadFile(agentAccessTokenFilePath(stateRoot))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file agentAccessTokenFile
	if err := json.Unmarshal(encoded, &file); err != nil {
		return nil, err
	}
	return file.AgentAccessTokens, nil
}

func saveAgentAccessTokens(stateRoot string, tokens []agentAccessTokenRecord) error {
	encoded, err := json.MarshalIndent(agentAccessTokenFile{AgentAccessTokens: tokens}, "", "  ")
	if err != nil {
		return err
	}
	path := agentAccessTokenFilePath(stateRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+agentAccessTokenFileName+".*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func bearerTokenSHA256Hex(bearerToken string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(bearerToken)))
	return hex.EncodeToString(digest[:])
}

// parseAllowedSourcesList reads a comma-separated source list; "all" or an
// empty list allows every source.
func parseAllowedSourcesList(allowedSourcesText string) (bool, []string) {
	seen := map[string]struct{}{}
	var sources []string
	for _, source := range strings.Split(allowedSourcesText, ",") {
		source = strings.ToLower(strings.TrimSpace(source))
		if source == agentAccessAllSourcesKeyword {
			return true, nil
		}
		if source == "" {
			continue
		}
		if _, duplicate := seen[source]; duplicate {
			continue
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	sort.Strings(sources)
	return len(sources) == 0, sources
}

func createAgentAccessToken(stateRoot, agentName, allowedSourcesText string, now time.Time) (agentAccessTokenRecord, string, error) {
	return createScopedAgentAccessToken(stateRoot, agentName, allowedSourcesText, agentAccessTokenSurfaceLocal, now, nil)
}

func createAgentAccessTokenForSurface(
	stateRoot, agentName, allowedSourcesText string,
	surface agentAccessTokenSurface,
	now time.Time,
) (agentAccessTokenRecord, string, error) {
	return createScopedAgentAccessToken(stateRoot, agentName, allowedSourcesText, surface, now, nil)
}

func createScopedAgentAccessToken(
	stateRoot, agentName, allowedSourcesText string,
	surface agentAccessTokenSurface,
	now time.Time,
	expiresTime *time.Time,
) (agentAccessTokenRecord, string, error) {
	if surface == "" {
		surface = agentAccessTokenSurfaceLocal
	}
	var createdRecord agentAccessTokenRecord
	var createdBearer string
	err := withAgentAccessStoreLock(stateRoot, func() error {
		tokens, err := loadAgentAccessTokens(stateRoot)
		if err != nil {
			return err
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		bearerToken := agentAccessBearerTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
		identifierBytes := make([]byte, 4)
		if _, err := rand.Read(identifierBytes); err != nil {
			return err
		}
		allSources, sources := parseAllowedSourcesList(allowedSourcesText)
		var expiresUTC *time.Time
		if expiresTime != nil {
			utc := expiresTime.UTC()
			expiresUTC = &utc
		}
		record := agentAccessTokenRecord{
			AgentAccessTokenIdentifier: AgentAccessTokenIdentifier("tok_" + hex.EncodeToString(identifierBytes)),
			AgentName:                  strings.TrimSpace(agentName),
			BearerTokenSHA256:          bearerTokenSHA256Hex(bearerToken),
			AllowedSurface:             surface,
			AllSourcesAllowed:          allSources,
			AllowedSources:             sources,
			CreatedTime:                now.UTC(),
			ExpiresTime:                expiresUTC,
		}
		if err := saveAgentAccessTokens(stateRoot, append(tokens, record)); err != nil {
			return err
		}
		createdRecord = record
		createdBearer = bearerToken
		return nil
	})
	if err != nil {
		return agentAccessTokenRecord{}, "", err
	}
	return createdRecord, createdBearer, nil
}

// isAgentAccessTokenIdentifierActive reports whether the named token exists and
// has not been revoked (regardless of short-lived access token expiry, so
// refresh tokens can check whether the owner revoked the grant).
func isAgentAccessTokenIdentifierActive(stateRoot string, tokenIdentifier AgentAccessTokenIdentifier) (bool, error) {
	tokens, err := loadAgentAccessTokens(stateRoot)
	if err != nil {
		return false, err
	}
	for _, token := range tokens {
		if token.AgentAccessTokenIdentifier == tokenIdentifier {
			return token.RevokedTime == nil, nil
		}
	}
	return false, nil
}

// replaceActiveAgentAccessTokenSecretLocked rotates the bearer token hash and
// expiry for an existing active grant while the caller holds the cross-process
// store lock (used by OAuth refresh token rotation so trawl token list stays
// one row per connected cloud agent).
func replaceActiveAgentAccessTokenSecretLocked(
	stateRoot string,
	tokenIdentifier AgentAccessTokenIdentifier,
	_ time.Time,
	expiresTime time.Time,
) (agentAccessTokenRecord, string, error) {
	tokens, err := loadAgentAccessTokens(stateRoot)
	if err != nil {
		return agentAccessTokenRecord{}, "", err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return agentAccessTokenRecord{}, "", err
	}
	bearerToken := agentAccessBearerTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	expiresUTC := expiresTime.UTC()
	for index := range tokens {
		if tokens[index].AgentAccessTokenIdentifier != tokenIdentifier {
			continue
		}
		if tokens[index].RevokedTime != nil {
			return agentAccessTokenRecord{}, "", errAgentAccessTokenNotRecognised
		}
		tokens[index].BearerTokenSHA256 = bearerTokenSHA256Hex(bearerToken)
		tokens[index].ExpiresTime = &expiresUTC
		if err := saveAgentAccessTokens(stateRoot, tokens); err != nil {
			return agentAccessTokenRecord{}, "", err
		}
		return tokens[index], bearerToken, nil
	}
	return agentAccessTokenRecord{}, "", errAgentAccessTokenNotRecognised
}

// revokeAgentAccessToken revokes one token by identifier and reports whether it
// existed.
func revokeAgentAccessToken(stateRoot string, tokenIdentifier AgentAccessTokenIdentifier, now time.Time) (bool, error) {
	var found bool
	err := withAgentAccessStoreLock(stateRoot, func() error {
		var innerErr error
		found, innerErr = revokeAgentAccessTokenLocked(stateRoot, tokenIdentifier, now)
		return innerErr
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

func revokeAgentAccessTokenLocked(stateRoot string, tokenIdentifier AgentAccessTokenIdentifier, now time.Time) (bool, error) {
	tokens, err := loadAgentAccessTokens(stateRoot)
	if err != nil {
		return false, err
	}
	found := false
	revokedTime := now.UTC()
	for index := range tokens {
		if tokens[index].AgentAccessTokenIdentifier != tokenIdentifier {
			continue
		}
		found = true
		if tokens[index].RevokedTime == nil {
			tokens[index].RevokedTime = &revokedTime
		}
	}
	if !found {
		return false, nil
	}
	return true, saveAgentAccessTokens(stateRoot, tokens)
}

// findActiveAgentAccessToken reads the token store on every call, so a
// revocation takes effect on the next request.
func findActiveAgentAccessToken(stateRoot, bearerToken string) (agentAccessTokenRecord, error) {
	if !strings.HasPrefix(strings.TrimSpace(bearerToken), agentAccessBearerTokenPrefix) {
		return agentAccessTokenRecord{}, errAgentAccessTokenNotRecognised
	}
	tokens, err := loadAgentAccessTokens(stateRoot)
	if err != nil {
		return agentAccessTokenRecord{}, err
	}
	wanted := []byte(bearerTokenSHA256Hex(bearerToken))
	now := time.Now().UTC()
	for _, token := range tokens {
		if subtle.ConstantTimeCompare([]byte(token.BearerTokenSHA256), wanted) == 1 && token.RevokedTime == nil {
			if token.ExpiresTime != nil && !now.Before(*token.ExpiresTime) {
				return agentAccessTokenRecord{}, errAgentAccessTokenNotRecognised
			}
			return token, nil
		}
	}
	return agentAccessTokenRecord{}, errAgentAccessTokenNotRecognised
}

func (token agentAccessTokenRecord) agentSourceScope() agentSourceScope {
	return newAgentSourceScope(token.AgentName, token.AllSourcesAllowed, token.AllowedSources)
}

func (token agentAccessTokenRecord) allowedSourcesDisplayText() string {
	if token.AllSourcesAllowed {
		return agentAccessAllSourcesKeyword
	}
	return strings.Join(token.AllowedSources, ", ")
}

// agentSourceScope is what one agent may read. Every agent MCP request runs
// with one, and trawler discovery returns only the sources it allows.
type agentSourceScope struct {
	agentName               string
	allSourcesAllowed       bool
	allowedSourceIdentities map[string]struct{}
}

func newAgentSourceScope(agentName string, allSourcesAllowed bool, allowedSources []string) agentSourceScope {
	scope := agentSourceScope{
		agentName:               agentName,
		allSourcesAllowed:       allSourcesAllowed,
		allowedSourceIdentities: map[string]struct{}{},
	}
	for _, source := range allowedSources {
		scope.allowedSourceIdentities[strings.ToLower(strings.TrimSpace(source))] = struct{}{}
	}
	return scope
}

func (scope agentSourceScope) allowsSource(trawlerIdentity string) bool {
	if scope.allSourcesAllowed {
		return true
	}
	_, allowed := scope.allowedSourceIdentities[strings.ToLower(strings.TrimSpace(trawlerIdentity))]
	return allowed
}

func (scope agentSourceScope) installedTrawlersAllowed(installedTrawlers []InstalledTrawler) []InstalledTrawler {
	allowed := make([]InstalledTrawler, 0, len(installedTrawlers))
	for _, trawler := range installedTrawlers {
		if scope.allowsSource(installedTrawlerIdentityText(trawler)) {
			allowed = append(allowed, trawler)
		}
	}
	return allowed
}

// agentServingContext marks a read-only request served by trawl mcp. It carries
// the trawlers found once at server start and the requesting agent's scope.
// Agent requests never mutate archives or the unified index.
type agentServingContext struct {
	installedTrawlersDiscoveredAtServerStart []InstalledTrawler
	agentSourceScope                         agentSourceScope
}

type agentServingContextKey struct{}

func withAgentServingContext(ctx context.Context, serving agentServingContext) context.Context {
	return context.WithValue(ctx, agentServingContextKey{}, serving)
}

func agentServingContextFrom(ctx context.Context) (agentServingContext, bool) {
	serving, found := ctx.Value(agentServingContextKey{}).(agentServingContext)
	return serving, found
}
