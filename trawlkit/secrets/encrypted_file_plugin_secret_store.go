package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/opentrawl/opentrawl/trawlkit"
)

const (
	encryptedPluginSecretFileName         = ".secrets.enc"
	encryptedPluginSecretKeyFileName      = "secrets.key"
	encryptedPluginSecretKeyByteCount     = 32
	encryptedPluginSecretAssociatedData   = "opentrawl encrypted plugin secret file"
	encryptedPluginSecretFilePermissions  = 0o600
	encryptedPluginSecretDirectoryPermits = 0o700
)

// EncryptedFilePluginSecretStore keeps plugin secrets in one AES-256-GCM
// sealed file. It is the secret store on platforms without a native keychain.
//
// The key lives in a separate 0600 file outside the OpenTrawl state root, so
// copying, backing up or syncing the state root never carries readable
// secrets. It does not protect against an attacker who can read the user's
// whole home directory; that needs an OS keyring or hardware key.
//
// AES-256-GCM comes from the Go standard library. XChaCha20-Poly1305 would
// add golang.org/x/crypto to every trawler module that links this package.
type EncryptedFilePluginSecretStore struct {
	encryptedSecretFilePath string
	secretFileKeyPath       string
	lock                    sync.Mutex
}

// NewEncryptedFilePluginSecretStore stores secrets in encryptedSecretFilePath
// sealed with the key at secretFileKeyPath, creating the key on first save.
func NewEncryptedFilePluginSecretStore(encryptedSecretFilePath, secretFileKeyPath string) *EncryptedFilePluginSecretStore {
	return &EncryptedFilePluginSecretStore{
		encryptedSecretFilePath: encryptedSecretFilePath,
		secretFileKeyPath:       secretFileKeyPath,
	}
}

// DefaultEncryptedFilePluginSecretStore uses $OPENTRAWL_STATE_ROOT/.secrets.enc
// and the key in the user configuration directory.
func DefaultEncryptedFilePluginSecretStore() (*EncryptedFilePluginSecretStore, error) {
	stateRoot, err := trawlkit.ResolveStateRoot("")
	if err != nil {
		return nil, err
	}
	userConfigurationDirectory, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find user configuration directory for the secret key: %w", err)
	}
	return NewEncryptedFilePluginSecretStore(
		filepath.Join(stateRoot, encryptedPluginSecretFileName),
		filepath.Join(userConfigurationDirectory, "opentrawl", encryptedPluginSecretKeyFileName),
	), nil
}

// pluginSecretsByServiceAndAccount is the plaintext inside the sealed file.
type pluginSecretsByServiceAndAccount map[string]map[string][]byte

func (store *EncryptedFilePluginSecretStore) LoadPluginSecret(serviceName, accountName string) ([]byte, error) {
	store.lock.Lock()
	defer store.lock.Unlock()
	secrets, err := store.readAllPluginSecrets(false)
	if err != nil {
		return nil, err
	}
	secretBytes, found := secrets[serviceName][accountName]
	if !found {
		return nil, ErrPluginSecretNotFound
	}
	return append([]byte(nil), secretBytes...), nil
}

func (store *EncryptedFilePluginSecretStore) SavePluginSecret(serviceName, accountName string, secretBytes []byte) error {
	store.lock.Lock()
	defer store.lock.Unlock()
	secrets, err := store.readAllPluginSecrets(true)
	if err != nil {
		return err
	}
	if secrets[serviceName] == nil {
		secrets[serviceName] = map[string][]byte{}
	}
	secrets[serviceName][accountName] = append([]byte(nil), secretBytes...)
	return store.writeAllPluginSecrets(secrets)
}

func (store *EncryptedFilePluginSecretStore) DeletePluginSecret(serviceName, accountName string) error {
	store.lock.Lock()
	defer store.lock.Unlock()
	secrets, err := store.readAllPluginSecrets(false)
	if err != nil {
		return err
	}
	if _, found := secrets[serviceName][accountName]; !found {
		return ErrPluginSecretNotFound
	}
	delete(secrets[serviceName], accountName)
	if len(secrets[serviceName]) == 0 {
		delete(secrets, serviceName)
	}
	return store.writeAllPluginSecrets(secrets)
}

func (store *EncryptedFilePluginSecretStore) readAllPluginSecrets(createKeyWhenMissing bool) (pluginSecretsByServiceAndAccount, error) {
	sealed, err := os.ReadFile(store.encryptedSecretFilePath)
	if errors.Is(err, fs.ErrNotExist) {
		if createKeyWhenMissing {
			if _, err := store.loadOrCreateSecretFileKey(); err != nil {
				return nil, err
			}
		}
		return pluginSecretsByServiceAndAccount{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read encrypted secret file: %w", err)
	}
	key, err := store.loadOrCreateSecretFileKey()
	if err != nil {
		return nil, err
	}
	aead, err := newEncryptedPluginSecretFileCipher(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, fmt.Errorf("encrypted secret file %s is truncated", store.encryptedSecretFilePath)
	}
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(encryptedPluginSecretAssociatedData))
	if err != nil {
		return nil, fmt.Errorf("decrypt %s with %s: the key is wrong or the file was changed", store.encryptedSecretFilePath, store.secretFileKeyPath)
	}
	secrets := pluginSecretsByServiceAndAccount{}
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return nil, fmt.Errorf("parse decrypted secret file: %w", err)
	}
	return secrets, nil
}

func (store *EncryptedFilePluginSecretStore) writeAllPluginSecrets(secrets pluginSecretsByServiceAndAccount) error {
	key, err := store.loadOrCreateSecretFileKey()
	if err != nil {
		return err
	}
	aead, err := newEncryptedPluginSecretFileCipher(key)
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(secrets)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, []byte(encryptedPluginSecretAssociatedData))
	return writeFileAtomicallyWithOwnerOnlyPermissions(store.encryptedSecretFilePath, sealed)
}

func (store *EncryptedFilePluginSecretStore) loadOrCreateSecretFileKey() ([]byte, error) {
	key, err := os.ReadFile(store.secretFileKeyPath)
	if err == nil {
		if len(key) != encryptedPluginSecretKeyByteCount {
			return nil, fmt.Errorf("secret key %s must be %d bytes", store.secretFileKeyPath, encryptedPluginSecretKeyByteCount)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read secret key: %w", err)
	}
	if _, statErr := os.Stat(store.encryptedSecretFilePath); statErr == nil {
		return nil, fmt.Errorf("secret key %s is missing, so %s cannot be read", store.secretFileKeyPath, store.encryptedSecretFilePath)
	}
	key = make([]byte, encryptedPluginSecretKeyByteCount)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := writeFileAtomicallyWithOwnerOnlyPermissions(store.secretFileKeyPath, key); err != nil {
		return nil, err
	}
	return key, nil
}

func newEncryptedPluginSecretFileCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func writeFileAtomicallyWithOwnerOnlyPermissions(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), encryptedPluginSecretDirectoryPermits); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(encryptedPluginSecretFilePermissions); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
