package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	googleAuthorizationEndpointURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpointURL         = "https://oauth2.googleapis.com/token"
	googleRevocationEndpointURL    = "https://oauth2.googleapis.com/revoke"
	GmailReadonlyScope             = "https://www.googleapis.com/auth/gmail.readonly"

	maximumGoogleTokenResponseBytes = 1 << 20
	maximumGoogleDPoPNonceBytes     = 4 << 10
)

var googleOAuthHTTPClient = &http.Client{Timeout: time.Minute}

// GoogleOAuthClient is this OpenTrawl build's Google desktop OAuth client.
// Google requires the desktop client secret at the token endpoint even with
// PKCE; it is not confidential in a distributed app.
type GoogleOAuthClient struct {
	GoogleOAuthClientIdentifier string
	GoogleOAuthClientSecret     string
}

type googleTokenEndpointResponse struct {
	AccessToken         string `json:"access_token"`
	RefreshToken        string `json:"refresh_token"`
	AccessTokenLifetime int64  `json:"expires_in"`
	GrantedScopes       string `json:"scope"`
}

type googleTokenEndpointError struct {
	HTTPStatusCode         int
	GoogleOAuthErrorCode   string
	GoogleOAuthDescription string
}

func (e googleTokenEndpointError) Error() string {
	return fmt.Sprintf("google token endpoint returned HTTP %d %s: %s", e.HTTPStatusCode, e.GoogleOAuthErrorCode, e.GoogleOAuthDescription)
}

// requestGoogleTokens posts one grant with a fresh DPoP proof. When Google
// asks for a server nonce, it retries once with the nonce Google sent. It
// returns the latest nonce even when the request fails, so the caller can
// keep it for the next request.
func requestGoogleTokens(ctx context.Context, grantForm url.Values, googleDPoPNonce string) (googleTokenEndpointResponse, string, error) {
	for attempt := 0; ; attempt++ {
		proof, err := signedDPoPProofForTokenEndpoint(googleTokenEndpointURL, googleDPoPNonce, time.Now())
		if err != nil {
			return googleTokenEndpointResponse{}, googleDPoPNonce, gmailConnectionStepFailure("DPoP proof", err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpointURL, strings.NewReader(grantForm.Encode()))
		if err != nil {
			return googleTokenEndpointResponse{}, googleDPoPNonce, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Accept", "application/json")
		request.Header.Set("DPoP", proof)
		response, err := googleOAuthHTTPClient.Do(request)
		if err != nil {
			return googleTokenEndpointResponse{}, googleDPoPNonce, gmailConnectionStepFailure("token request", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maximumGoogleTokenResponseBytes))
		closeErr := response.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return googleTokenEndpointResponse{}, googleDPoPNonce, gmailConnectionStepFailure("token response", err)
		}
		receivedNewNonce := false
		if nonce := response.Header.Get("DPoP-Nonce"); nonce != "" && len(nonce) <= maximumGoogleDPoPNonceBytes {
			receivedNewNonce = nonce != googleDPoPNonce
			googleDPoPNonce = nonce
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			var tokens googleTokenEndpointResponse
			if err := json.Unmarshal(body, &tokens); err != nil {
				return googleTokenEndpointResponse{}, googleDPoPNonce, gmailConnectionStepFailure("token response", err)
			}
			if tokens.AccessToken == "" {
				return googleTokenEndpointResponse{}, googleDPoPNonce, gmailConnectionStepFailure("token response", errors.New("google returned no access token"))
			}
			return tokens, googleDPoPNonce, nil
		}
		var endpointErrorBody struct {
			GoogleOAuthErrorCode   string `json:"error"`
			GoogleOAuthDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &endpointErrorBody)
		endpointError := googleTokenEndpointError{
			HTTPStatusCode:         response.StatusCode,
			GoogleOAuthErrorCode:   endpointErrorBody.GoogleOAuthErrorCode,
			GoogleOAuthDescription: endpointErrorBody.GoogleOAuthDescription,
		}
		if endpointError.GoogleOAuthErrorCode == "use_dpop_nonce" && receivedNewNonce && attempt == 0 {
			continue
		}
		if endpointError.GoogleOAuthErrorCode == "invalid_grant" {
			return googleTokenEndpointResponse{}, googleDPoPNonce, gmailConnectionFailure(GoogleNoLongerAcceptsGmailGrant, "token request", endpointError)
		}
		return googleTokenEndpointResponse{}, googleDPoPNonce, gmailConnectionStepFailure("token request", endpointError)
	}
}

// revokeGoogleRefreshToken treats HTTP 400 as already revoked: Google
// answers 400 for a token it no longer knows.
func revokeGoogleRefreshToken(ctx context.Context, refreshToken string) error {
	form := url.Values{"token": {refreshToken}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, googleRevocationEndpointURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := googleOAuthHTTPClient.Do(request)
	if err != nil {
		return gmailConnectionStepFailure("token revocation", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumGoogleTokenResponseBytes))
	_ = response.Body.Close()
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusBadRequest {
		return nil
	}
	return gmailConnectionStepFailure("token revocation", fmt.Errorf("google revocation endpoint returned HTTP %d", response.StatusCode))
}

func grantedScopesIncludeGmailReadonly(grantedScopes string) bool {
	for _, scope := range strings.Fields(grantedScopes) {
		if scope == GmailReadonlyScope {
			return true
		}
	}
	return false
}
