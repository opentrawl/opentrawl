package secrets

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedFilePluginSecretStoreKeepsSecretsSealedOwnerOnlyAndReadableOnlyWithItsKey(t *testing.T) {
	directory := t.TempDir()
	secretFile := filepath.Join(directory, "state", ".secrets.enc")
	keyFile := filepath.Join(directory, "config", "secrets.key")
	store := NewEncryptedFilePluginSecretStore(secretFile, keyFile)
	secret := make([]byte, 48)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePluginSecret("org.opentrawl.plugin.telegram", "session", secret); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewEncryptedFilePluginSecretStore(secretFile, keyFile).LoadPluginSecret("org.opentrawl.plugin.telegram", "session")
	if err != nil || !bytes.Equal(loaded, secret) {
		t.Fatalf("load = %x, %v; want saved secret", loaded, err)
	}
	for _, path := range []string{secretFile, keyFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", path, info.Mode().Perm())
		}
	}
	sealed, err := os.ReadFile(secretFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, secret[:16]) || bytes.Contains(sealed, []byte("telegram")) {
		t.Fatal("sealed secret file contains plaintext")
	}

	otherKeyFile := filepath.Join(directory, "other.key")
	otherKey := make([]byte, encryptedPluginSecretKeyByteCount)
	if _, err := rand.Read(otherKey); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherKeyFile, otherKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEncryptedFilePluginSecretStore(secretFile, otherKeyFile).LoadPluginSecret("org.opentrawl.plugin.telegram", "session"); err == nil {
		t.Fatal("wrong key opened the sealed secret file")
	}

	if err := store.DeletePluginSecret("org.opentrawl.plugin.telegram", "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadPluginSecret("org.opentrawl.plugin.telegram", "session"); !errors.Is(err, ErrPluginSecretNotFound) {
		t.Fatalf("load after delete = %v, want not found", err)
	}
}
