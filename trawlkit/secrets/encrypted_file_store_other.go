//go:build !darwin

package secrets

// NewPlatformPluginSecretStore returns the encrypted file store on platforms
// without the macOS Data Protection Keychain. If the state root or user
// configuration directory cannot be found, every call reports that the store
// is unavailable.
func NewPlatformPluginSecretStore() PluginSecretStore {
	store, err := DefaultEncryptedFilePluginSecretStore()
	if err != nil {
		return unavailablePluginSecretStore{}
	}
	return store
}

type unavailablePluginSecretStore struct{}

func (unavailablePluginSecretStore) LoadPluginSecret(string, string) ([]byte, error) {
	return nil, ErrPluginSecretStoreUnavailable
}

func (unavailablePluginSecretStore) SavePluginSecret(string, string, []byte) error {
	return ErrPluginSecretStoreUnavailable
}

func (unavailablePluginSecretStore) DeletePluginSecret(string, string) error {
	return ErrPluginSecretStoreUnavailable
}
