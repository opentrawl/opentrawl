package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"time"

	gmailcredential "github.com/opentrawl/opentrawl/gmail/proto/trawl/gmail/credential"
)

const (
	googleSignInCallbackPath = "/oauth2/callback"
	googleSignInTimeout      = 5 * time.Minute
)

// GmailGoogleGrant is a completed Google sign-in that is not yet stored. The
// caller checks the account with GmailAPIAccessToken, then calls
// StoreInKeychain or Discard.
type GmailGoogleGrant struct {
	GmailAPIAccessToken     string
	googleOAuthRefreshToken string
	latestGoogleDPoPNonce   string
}

type googleSignInCallbackResult struct {
	authorizationCode string
	err               error
}

// GoogleSignInBrowserPages holds the library-worded pages the browser shows
// when Google returns to the loopback callback.
type GoogleSignInBrowserPages struct {
	SignInReceivedPageText string
	StateMismatchPageText  string
}

// SignInToGoogleForGmail runs the loopback OAuth flow with PKCE in the
// person's browser and creates the Secure Enclave key that binds the grant
// to this Mac.
func SignInToGoogleForGmail(ctx context.Context, client GoogleOAuthClient, browserPages GoogleSignInBrowserPages) (*GmailGoogleGrant, error) {
	if client.GoogleOAuthClientIdentifier == "" || client.GoogleOAuthClientSecret == "" {
		return nil, gmailConnectionFailure(OpenTrawlBuildHasNoGoogleOAuthClient, "configuration", nil)
	}
	if _, err := loadStoredGmailCredential(); err == nil {
		return nil, gmailConnectionFailure(GmailIsAlreadyConnected, "credential check", nil)
	} else if !errors.Is(err, ErrGmailCredentialNotFound) {
		return nil, gmailConnectionStepFailure("credential check", err)
	}
	// The key is made before the browser opens, so a build that cannot use
	// the Secure Enclave fails before the person signs in.
	if err := createGmailDPoPKey(); err != nil {
		return nil, gmailConnectionStepFailure("Secure Enclave key", err)
	}
	keepDPoPKey := false
	defer func() {
		if !keepDPoPKey {
			_ = deleteGmailDPoPKey()
		}
	}()
	state, err := randomBase64URLText()
	if err != nil {
		return nil, err
	}
	codeVerifier, err := randomBase64URLText()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, gmailConnectionStepFailure("loopback listener", err)
	}
	redirectURI := "http://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port) + googleSignInCallbackPath
	callbackResults := make(chan googleSignInCallbackResult, 1)
	server := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           googleSignInCallbackHandler(state, browserPages, callbackResults),
	}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	codeChallenge := sha256.Sum256([]byte(codeVerifier))
	authorizationURL := googleAuthorizationEndpointURL + "?" + url.Values{
		"client_id":             {client.GoogleOAuthClientIdentifier},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {GmailReadonlyScope},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(codeChallenge[:])},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}.Encode()
	if err := exec.CommandContext(ctx, "/usr/bin/open", authorizationURL).Run(); err != nil {
		return nil, gmailConnectionStepFailure("browser", err)
	}

	waitContext, cancelWait := context.WithTimeout(ctx, googleSignInTimeout)
	defer cancelWait()
	var callback googleSignInCallbackResult
	select {
	case callback = <-callbackResults:
	case <-waitContext.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, gmailConnectionFailure(GoogleSignInTimedOut, "browser sign-in", waitContext.Err())
	}
	if callback.err != nil {
		return nil, callback.err
	}
	grant, err := exchangeAuthorizationCode(ctx, client, callback.authorizationCode, codeVerifier, redirectURI)
	keepDPoPKey = err == nil
	return grant, err
}

func googleSignInCallbackHandler(state string, browserPages GoogleSignInBrowserPages, callbackResults chan<- googleSignInCallbackResult) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != googleSignInCallbackPath {
			http.NotFound(writer, request)
			return
		}
		query := request.URL.Query()
		if query.Get("state") != state {
			http.Error(writer, browserPages.StateMismatchPageText, http.StatusBadRequest)
			return
		}
		var result googleSignInCallbackResult
		switch {
		case query.Get("error") == "access_denied":
			result.err = gmailConnectionFailure(GoogleSignInWasCancelled, "browser sign-in", nil)
		case query.Get("error") != "" || query.Get("code") == "":
			result.err = gmailConnectionStepFailure("callback", errors.New("google sign-in returned "+query.Get("error")))
		default:
			result.authorizationCode = query.Get("code")
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte(browserPages.SignInReceivedPageText))
		select {
		case callbackResults <- result:
		default:
		}
	})
}

func exchangeAuthorizationCode(ctx context.Context, client GoogleOAuthClient, authorizationCode, codeVerifier, redirectURI string) (*GmailGoogleGrant, error) {
	tokens, nonce, err := requestGoogleTokens(ctx, url.Values{
		"client_id":     {client.GoogleOAuthClientIdentifier},
		"client_secret": {client.GoogleOAuthClientSecret},
		"code":          {authorizationCode},
		"code_verifier": {codeVerifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}, "")
	if err != nil {
		return nil, err
	}
	if tokens.RefreshToken == "" {
		return nil, gmailConnectionStepFailure("token response", errors.New("google returned no refresh token"))
	}
	if !grantedScopesIncludeGmailReadonly(tokens.GrantedScopes) {
		_ = revokeGoogleRefreshToken(ctx, tokens.RefreshToken)
		return nil, gmailConnectionFailure(GoogleSignInDidNotGrantGmailReadAccess, "token response", nil)
	}
	return &GmailGoogleGrant{
		GmailAPIAccessToken:     tokens.AccessToken,
		googleOAuthRefreshToken: tokens.RefreshToken,
		latestGoogleDPoPNonce:   nonce,
	}, nil
}

// StoreInKeychain keeps the grant. If the Keychain refuses it, the grant is
// revoked so no live refresh token is left behind.
func (grant *GmailGoogleGrant) StoreInKeychain(ctx context.Context) error {
	err := saveStoredGmailCredential(&gmailcredential.StoredGmailGoogleOAuthCredential{
		GoogleOauthRefreshToken: grant.googleOAuthRefreshToken,
		LatestGoogleDpopNonce:   grant.latestGoogleDPoPNonce,
	})
	if err != nil {
		grant.Discard(ctx)
		return gmailConnectionStepFailure("Keychain save", err)
	}
	return nil
}

// Discard revokes the grant at Google and deletes its Secure Enclave key.
func (grant *GmailGoogleGrant) Discard(ctx context.Context) {
	_ = revokeGoogleRefreshToken(ctx, grant.googleOAuthRefreshToken)
	_ = deleteGmailDPoPKey()
}

// DisconnectGmail revokes the grant at Google, then removes the Keychain
// item and the Secure Enclave key. The local archive is not touched.
func DisconnectGmail(ctx context.Context) error {
	credential, err := loadStoredGmailCredential()
	switch {
	case errors.Is(err, ErrGmailCredentialNotFound):
	case err != nil:
		return gmailConnectionStepFailure("Keychain load", err)
	default:
		if err := revokeGoogleRefreshToken(ctx, credential.GetGoogleOauthRefreshToken()); err != nil {
			return err
		}
		if err := deleteStoredGmailCredential(); err != nil {
			return gmailConnectionStepFailure("Keychain delete", err)
		}
	}
	if err := deleteGmailDPoPKey(); err != nil {
		return gmailConnectionStepFailure("Secure Enclave key delete", err)
	}
	return nil
}

func randomBase64URLText() (string, error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}
