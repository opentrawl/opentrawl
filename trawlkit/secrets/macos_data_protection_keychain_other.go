//go:build !darwin

package secrets

type unavailablePluginSecretStore struct{}

// NewPlatformPluginSecretStore returns an unavailable store on platforms
// without a configured native secret backend.
func NewPlatformPluginSecretStore() PluginSecretStore {
	return unavailablePluginSecretStore{}
}

func (unavailablePluginSecretStore) LoadPluginSecret(string, string) ([]byte, error) {
	return nil, ErrPluginSecretStoreUnavailable
}

func (unavailablePluginSecretStore) SavePluginSecret(string, string, []byte) error {
	return ErrPluginSecretStoreUnavailable
}

func (unavailablePluginSecretStore) DeletePluginSecret(string, string) error {
	return ErrPluginSecretStoreUnavailable
}
