//go:build !darwin

package auth

func saveGenericPasswordInDataProtectionKeychain(string, string, []byte) error {
	return ErrMacOSKeychainNeedsSignedApp
}

func loadGenericPasswordFromDataProtectionKeychain(string, string) ([]byte, error) {
	return nil, ErrMacOSKeychainNeedsSignedApp
}

func deleteGenericPasswordFromDataProtectionKeychain(string, string) error {
	return ErrMacOSKeychainNeedsSignedApp
}

func createSecureEnclaveSigningKey(string) error {
	return ErrMacOSKeychainNeedsSignedApp
}

func copySecureEnclaveSigningKeyPublicKey(string) ([]byte, error) {
	return nil, ErrMacOSKeychainNeedsSignedApp
}

func signWithSecureEnclaveSigningKey(string, []byte) ([]byte, error) {
	return nil, ErrMacOSKeychainNeedsSignedApp
}

func deleteSecureEnclaveSigningKey(string) error {
	return ErrMacOSKeychainNeedsSignedApp
}
