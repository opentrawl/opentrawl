//go:build darwin

package secrets

/*
#cgo LDFLAGS: -framework Foundation -framework Security -framework LocalAuthentication
#include <stdlib.h>
#include "macos_data_protection_keychain_native.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	macOSKeychainStatusSuccess            int32 = 0
	macOSKeychainStatusItemNotFound       int32 = -25300
	macOSKeychainStatusMissingEntitlement int32 = -34018
)

type macOSDataProtectionKeychainStore struct{}

// NewPlatformPluginSecretStore returns the macOS Data Protection Keychain
// secret store.
func NewPlatformPluginSecretStore() PluginSecretStore {
	return macOSDataProtectionKeychainStore{}
}

func (macOSDataProtectionKeychainStore) LoadPluginSecret(serviceName string, accountName string) ([]byte, error) {
	serviceText := C.CString(serviceName)
	accountText := C.CString(accountName)
	defer C.free(unsafe.Pointer(serviceText))
	defer C.free(unsafe.Pointer(accountText))

	result := C.loadPluginSecretFromDataProtectionKeychain(serviceText, accountText)
	defer C.freePluginKeychainBytes(result.returned_bytes)
	if err := translateMacOSKeychainStatus("load", int32(result.keychain_status)); err != nil {
		return nil, err
	}
	return C.GoBytes(unsafe.Pointer(result.returned_bytes), C.int(result.returned_byte_count)), nil
}

func (macOSDataProtectionKeychainStore) SavePluginSecret(serviceName string, accountName string, secretBytes []byte) error {
	serviceText := C.CString(serviceName)
	accountText := C.CString(accountName)
	defer C.free(unsafe.Pointer(serviceText))
	defer C.free(unsafe.Pointer(accountText))

	var dataPointer *C.uint8_t
	if len(secretBytes) > 0 {
		dataPointer = (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(secretBytes)))
	}
	status := C.savePluginSecretInDataProtectionKeychain(serviceText, accountText, dataPointer, C.size_t(len(secretBytes)))
	return translateMacOSKeychainStatus("save", int32(status))
}

func (macOSDataProtectionKeychainStore) DeletePluginSecret(serviceName string, accountName string) error {
	serviceText := C.CString(serviceName)
	accountText := C.CString(accountName)
	defer C.free(unsafe.Pointer(serviceText))
	defer C.free(unsafe.Pointer(accountText))

	status := C.deletePluginSecretFromDataProtectionKeychain(serviceText, accountText)
	return translateMacOSKeychainStatus("delete", int32(status))
}

func translateMacOSKeychainStatus(operation string, status int32) error {
	switch status {
	case macOSKeychainStatusSuccess:
		return nil
	case macOSKeychainStatusItemNotFound:
		return ErrPluginSecretNotFound
	case macOSKeychainStatusMissingEntitlement:
		return fmt.Errorf("%w (OSStatus %d)", ErrPluginSecretStoreUnavailable, status)
	default:
		return fmt.Errorf("macOS Data Protection Keychain %s failed (OSStatus %d)", operation, status)
	}
}
