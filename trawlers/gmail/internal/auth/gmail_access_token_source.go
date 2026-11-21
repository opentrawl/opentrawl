package auth

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"
)

// accessTokenRefreshMargin renews an access token before Google rejects it,
// leaving headroom for large raw-message downloads and retry waits.
const accessTokenRefreshMargin = 2 * time.Minute

// GmailAccessTokenSource keeps the Gmail API access token in memory only and
// renews it from the Keychain grant with a DPoP-bound refresh request.
// It pins the refresh token observed when the update began so a concurrent
// disconnect and reconnect to another Google account cannot cross accounts
// mid-update.
type GmailAccessTokenSource struct {
	client             GoogleOAuthClient
	mutex              sync.Mutex
	pinnedRefreshToken string
	accessToken        string
	expiresAt          time.Time
}

// NewGmailAccessTokenSource fails at once when this Mac holds no grant, so
// an update never starts without one.
func NewGmailAccessTokenSource(client GoogleOAuthClient) (*GmailAccessTokenSource, error) {
	credential, err := loadStoredGmailCredential()
	if errors.Is(err, ErrGmailCredentialNotFound) {
		return nil, gmailConnectionFailure(GmailIsNotConnected, "credential check", nil)
	}
	if err != nil {
		return nil, gmailConnectionStepFailure("Keychain load", err)
	}
	return &GmailAccessTokenSource{
		client:             client,
		pinnedRefreshToken: credential.GetGoogleOauthRefreshToken(),
	}, nil
}

func (source *GmailAccessTokenSource) GmailAPIAccessToken(ctx context.Context) (string, error) {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	if source.accessToken != "" && time.Now().Add(accessTokenRefreshMargin).Before(source.expiresAt) {
		return source.accessToken, nil
	}
	credential, err := loadStoredGmailCredential()
	if errors.Is(err, ErrGmailCredentialNotFound) {
		return "", gmailConnectionFailure(GmailIsNotConnected, "credential check", nil)
	}
	if err != nil {
		return "", gmailConnectionStepFailure("Keychain load", err)
	}
	if credential.GetGoogleOauthRefreshToken() != source.pinnedRefreshToken {
		return "", gmailConnectionFailure(GoogleNoLongerAcceptsGmailGrant, "credential check", nil)
	}
	tokens, nonce, err := requestGoogleTokens(ctx, googleDPoPGrantRefreshToken, url.Values{
		"client_id":     {source.client.GoogleOAuthClientIdentifier},
		"client_secret": {source.client.GoogleOAuthClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.GetGoogleOauthRefreshToken()},
	}, credential.GetLatestGoogleDpopNonce())
	// Google may report invalid_grant for a key-binding problem as well as a
	// revoked grant, so the Keychain item and key stay; disconnect can still
	// revoke them.
	changed := nonce != credential.GetLatestGoogleDpopNonce()
	credential.LatestGoogleDpopNonce = nonce
	if err == nil && tokens.RefreshToken != "" && tokens.RefreshToken != credential.GetGoogleOauthRefreshToken() {
		credential.GoogleOauthRefreshToken = tokens.RefreshToken
		changed = true
	}
	if changed {
		if saveErr := saveStoredGmailCredential(credential); saveErr != nil && err == nil {
			return "", gmailConnectionStepFailure("Keychain save", saveErr)
		}
		source.pinnedRefreshToken = credential.GetGoogleOauthRefreshToken()
	}
	if err != nil {
		return "", err
	}
	source.accessToken = tokens.AccessToken
	source.expiresAt = time.Now().Add(time.Duration(tokens.AccessTokenLifetime) * time.Second)
	return source.accessToken, nil
}

// InvalidateRejectedGmailAPIAccessToken drops the cached access token when
// Gmail rejects it with HTTP 401 so the next GmailAPIAccessToken call renews
// it from the pinned Keychain grant. It leaves a newer token intact when
// another worker already refreshed it, and reports that a retry can proceed
// with a fresh token.
func (source *GmailAccessTokenSource) InvalidateRejectedGmailAPIAccessToken(rejectedAccessToken string) bool {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	if source.accessToken == rejectedAccessToken {
		source.accessToken = ""
		source.expiresAt = time.Time{}
	}
	return true
}
