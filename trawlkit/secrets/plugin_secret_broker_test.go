package secrets

import (
	"bytes"
	"errors"
	"testing"
)

type inMemoryPluginSecretStore struct {
	entries map[string][]byte
}

func newInMemoryPluginSecretStore() *inMemoryPluginSecretStore {
	return &inMemoryPluginSecretStore{entries: make(map[string][]byte)}
}

func (store *inMemoryPluginSecretStore) LoadPluginSecret(serviceName string, accountName string) ([]byte, error) {
	value, ok := store.entries[serviceName+"\x00"+accountName]
	if !ok {
		return nil, ErrPluginSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *inMemoryPluginSecretStore) SavePluginSecret(serviceName string, accountName string, secretBytes []byte) error {
	store.entries[serviceName+"\x00"+accountName] = append([]byte(nil), secretBytes...)
	return nil
}

func (store *inMemoryPluginSecretStore) DeletePluginSecret(serviceName string, accountName string) error {
	key := serviceName + "\x00" + accountName
	if _, ok := store.entries[key]; !ok {
		return ErrPluginSecretNotFound
	}
	delete(store.entries, key)
	return nil
}

func TestPluginSecretBrokerEnforcesPluginNamespaceAndKeyAllowlists(t *testing.T) {
	backingStore := newInMemoryPluginSecretStore()
	alphaBroker, err := NewPluginSecretBroker(backingStore, "alpha", []string{"token"}, []string{"token"})
	if err != nil {
		t.Fatalf("NewPluginSecretBroker(alpha) failed: %v", err)
	}
	betaBroker, err := NewPluginSecretBroker(backingStore, "beta", []string{"token"}, nil)
	if err != nil {
		t.Fatalf("NewPluginSecretBroker(beta) failed: %v", err)
	}

	if err := alphaBroker.ApplySecretMutation("token", []byte("alpha-secret"), false); err != nil {
		t.Fatalf("alpha write approved key failed: %v", err)
	}
	if err := alphaBroker.ApplySecretMutation("unapproved", []byte("x"), false); err == nil {
		t.Fatalf("expected error writing unapproved secret key")
	}
	if err := betaBroker.ApplySecretMutation("token", []byte("x"), false); err == nil {
		t.Fatalf("expected error writing read-only secret key")
	}

	alphaLoaded, err := alphaBroker.LoadApprovedSecrets()
	if err != nil {
		t.Fatalf("alpha load failed: %v", err)
	}
	if !bytes.Equal(alphaLoaded["token"], []byte("alpha-secret")) {
		t.Fatalf("alpha token = %q, want %q", alphaLoaded["token"], "alpha-secret")
	}

	betaLoaded, err := betaBroker.LoadApprovedSecrets()
	if err != nil {
		t.Fatalf("beta load failed: %v", err)
	}
	if len(betaLoaded) != 0 {
		t.Fatalf("beta read another plugin's namespace: got %d secrets", len(betaLoaded))
	}

	if err := alphaBroker.ApplySecretMutation("token", nil, true); err != nil {
		t.Fatalf("alpha delete approved key failed: %v", err)
	}
	alphaAfterDelete, err := alphaBroker.LoadApprovedSecrets()
	if err != nil {
		t.Fatalf("alpha load after delete failed: %v", err)
	}
	if len(alphaAfterDelete) != 0 {
		t.Fatalf("expected secret to be deleted, still have %d entries", len(alphaAfterDelete))
	}
}

func TestPluginIdentifierAndSecretKeyValidation(t *testing.T) {
	if _, err := PluginKeychainServiceName("../escape"); err == nil {
		t.Fatalf("expected invalid plugin identifier error")
	}
	if err := ValidatePluginSecretKey("INVALID/KEY"); err == nil {
		t.Fatalf("expected invalid secret key error")
	}
	service, err := PluginKeychainServiceName("money")
	if err != nil {
		t.Fatalf("valid plugin identifier rejected: %v", err)
	}
	if service != "org.opentrawl.plugin.money" {
		t.Fatalf("service = %q, want org.opentrawl.plugin.money", service)
	}
	if _, err := newInMemoryPluginSecretStore().LoadPluginSecret(service, "missing"); !errors.Is(err, ErrPluginSecretNotFound) {
		t.Fatalf("expected ErrPluginSecretNotFound, got %v", err)
	}
}
