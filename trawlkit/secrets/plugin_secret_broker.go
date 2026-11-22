// Package secrets brokers isolated per-plugin credentials between the OpenTrawl
// host Keychain and sandboxed standalone plugin executables.
package secrets

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const pluginKeychainServicePrefix = "org.opentrawl.plugin."

var (
	validPluginIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	validPluginSecretKeyPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
)

// ErrPluginSecretNotFound reports that no secret is stored for the requested
// plugin identifier and secret key.
var ErrPluginSecretNotFound = errors.New("plugin secret not found")

// ErrPluginSecretStoreUnavailable reports that the host secret store cannot be
// accessed in the current signing or platform environment.
var ErrPluginSecretStoreUnavailable = errors.New("plugin secret store is unavailable")

// PluginSecretStore is the narrow storage seam implemented by the macOS Data
// Protection Keychain and by isolated test stores.
type PluginSecretStore interface {
	LoadPluginSecret(serviceName string, accountName string) ([]byte, error)
	SavePluginSecret(serviceName string, accountName string, secretBytes []byte) error
	DeletePluginSecret(serviceName string, accountName string) error
}

// PluginSecretBroker enforces per-plugin Keychain namespace isolation and
// approved read/write key sets for one plugin.
type PluginSecretBroker struct {
	store            PluginSecretStore
	pluginIdentifier string
	keychainService  string
	allowedReadKeys  map[string]struct{}
	allowedWriteKeys map[string]struct{}
}

// ValidatePluginIdentifier checks that a plugin identifier is a safe lowercase
// slug suitable for Keychain service namespacing and filesystem paths.
func ValidatePluginIdentifier(pluginIdentifier string) error {
	if !validPluginIdentifierPattern.MatchString(pluginIdentifier) {
		return fmt.Errorf("plugin identifier %q must match ^[a-z][a-z0-9-]{0,31}$", pluginIdentifier)
	}
	return nil
}

// ValidatePluginSecretKey checks that a secret key uses only safe lowercase
// characters and has no path separators or control characters.
func ValidatePluginSecretKey(secretKey string) error {
	if !validPluginSecretKeyPattern.MatchString(secretKey) {
		return fmt.Errorf("plugin secret key %q must match ^[a-z0-9][a-z0-9_.-]{0,63}$", secretKey)
	}
	return nil
}

// PluginKeychainServiceName returns the canonical Data Protection Keychain
// service name scoped exclusively to one plugin identifier.
func PluginKeychainServiceName(pluginIdentifier string) (string, error) {
	if err := ValidatePluginIdentifier(pluginIdentifier); err != nil {
		return "", err
	}
	return pluginKeychainServicePrefix + pluginIdentifier, nil
}

// NewPluginSecretBroker constructs a secret broker for one plugin. When store
// is nil, the platform's default secret store is used.
func NewPluginSecretBroker(
	store PluginSecretStore,
	pluginIdentifier string,
	approvedReadSecretKeys []string,
	approvedWriteSecretKeys []string,
) (*PluginSecretBroker, error) {
	serviceName, err := PluginKeychainServiceName(pluginIdentifier)
	if err != nil {
		return nil, err
	}
	if store == nil {
		store = NewPlatformPluginSecretStore()
	}
	readKeys := make(map[string]struct{}, len(approvedReadSecretKeys))
	for _, rawKey := range approvedReadSecretKeys {
		key := strings.TrimSpace(rawKey)
		if err := ValidatePluginSecretKey(key); err != nil {
			return nil, fmt.Errorf("approved read secret key: %w", err)
		}
		readKeys[key] = struct{}{}
	}
	writeKeys := make(map[string]struct{}, len(approvedWriteSecretKeys))
	for _, rawKey := range approvedWriteSecretKeys {
		key := strings.TrimSpace(rawKey)
		if err := ValidatePluginSecretKey(key); err != nil {
			return nil, fmt.Errorf("approved write secret key: %w", err)
		}
		writeKeys[key] = struct{}{}
	}
	return &PluginSecretBroker{
		store:            store,
		pluginIdentifier: pluginIdentifier,
		keychainService:  serviceName,
		allowedReadKeys:  readKeys,
		allowedWriteKeys: writeKeys,
	}, nil
}

// LoadApprovedSecrets reads all approved readable secrets that currently exist
// for this plugin. Missing secrets are omitted without error.
func (broker *PluginSecretBroker) LoadApprovedSecrets() (map[string][]byte, error) {
	if broker == nil || len(broker.allowedReadKeys) == 0 {
		return nil, nil
	}
	loaded := make(map[string][]byte, len(broker.allowedReadKeys))
	for key := range broker.allowedReadKeys {
		secretBytes, err := broker.store.LoadPluginSecret(broker.keychainService, key)
		if errors.Is(err, ErrPluginSecretNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load secret %q for plugin %q: %w", key, broker.pluginIdentifier, err)
		}
		copied := make([]byte, len(secretBytes))
		copy(copied, secretBytes)
		loaded[key] = copied
	}
	return loaded, nil
}

// ApplySecretMutation stores or deletes one secret key after verifying the key
// belongs to this plugin's approved writable secret set.
func (broker *PluginSecretBroker) ApplySecretMutation(secretKey string, secretBytes []byte, deleteSecret bool) error {
	if broker == nil {
		return errors.New("plugin secret broker is nil")
	}
	key := strings.TrimSpace(secretKey)
	if err := ValidatePluginSecretKey(key); err != nil {
		return err
	}
	if _, allowed := broker.allowedWriteKeys[key]; !allowed {
		return fmt.Errorf("plugin %q is not approved to write secret key %q", broker.pluginIdentifier, key)
	}
	if deleteSecret {
		err := broker.store.DeletePluginSecret(broker.keychainService, key)
		if errors.Is(err, ErrPluginSecretNotFound) {
			return nil
		}
		return err
	}
	return broker.store.SavePluginSecret(broker.keychainService, key, secretBytes)
}
