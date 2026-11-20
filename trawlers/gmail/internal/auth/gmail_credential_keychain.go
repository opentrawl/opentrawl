package auth

import (
	"errors"
	"fmt"

	gmailcredential "github.com/opentrawl/opentrawl/gmail/proto/trawl/gmail/credential"
	"google.golang.org/protobuf/proto"
)

// The Google grant lives in the signed app's default access group of the
// non-synchronising Data Protection Keychain. It never falls back to the
// file-based login keychain.
const (
	gmailCredentialKeychainService = "com.opentrawl.gmail"
	gmailCredentialKeychainAccount = "oauth-refresh-token"
	gmailDPoPKeyApplicationTag     = "com.opentrawl.gmail.dpop-key"
)

var ErrGmailCredentialNotFound = errors.New("no Gmail credential in the Keychain")

func loadStoredGmailCredential() (*gmailcredential.StoredGmailGoogleOAuthCredential, error) {
	encoded, err := loadGenericPasswordFromDataProtectionKeychain(gmailCredentialKeychainService, gmailCredentialKeychainAccount)
	if errors.Is(err, ErrMacOSKeychainItemNotFound) {
		return nil, ErrGmailCredentialNotFound
	}
	if err != nil {
		return nil, err
	}
	credential := &gmailcredential.StoredGmailGoogleOAuthCredential{}
	if err := proto.Unmarshal(encoded, credential); err != nil {
		return nil, fmt.Errorf("decode stored Gmail credential: %w", err)
	}
	if credential.GetGoogleOauthRefreshToken() == "" {
		return nil, errors.New("stored Gmail credential has no refresh token")
	}
	return credential, nil
}

func saveStoredGmailCredential(credential *gmailcredential.StoredGmailGoogleOAuthCredential) error {
	encoded, err := proto.Marshal(credential)
	if err != nil {
		return err
	}
	return saveGenericPasswordInDataProtectionKeychain(gmailCredentialKeychainService, gmailCredentialKeychainAccount, encoded)
}

func deleteStoredGmailCredential() error {
	err := deleteGenericPasswordFromDataProtectionKeychain(gmailCredentialKeychainService, gmailCredentialKeychainAccount)
	if errors.Is(err, ErrMacOSKeychainItemNotFound) {
		return nil
	}
	return err
}

// GmailCredentialIsStored reports whether this Mac holds a Gmail grant. It
// never shows a Keychain prompt.
func GmailCredentialIsStored() (bool, error) {
	_, err := loadStoredGmailCredential()
	if errors.Is(err, ErrGmailCredentialNotFound) {
		return false, nil
	}
	return err == nil, err
}
