package auth

import (
	"errors"
	"fmt"
)

const (
	macOSKeychainStatusSuccess            int32 = 0
	macOSKeychainStatusItemNotFound       int32 = -25300
	macOSKeychainStatusMissingEntitlement int32 = -34018
)

var (
	ErrMacOSKeychainItemNotFound = errors.New("macOS Keychain item not found")
	// ErrMacOSKeychainNeedsSignedApp is what the Data Protection Keychain
	// returns to a binary without an application identifier, such as an
	// ad hoc or unsigned development build.
	ErrMacOSKeychainNeedsSignedApp = errors.New("the Data Protection Keychain needs the signed OpenTrawl app")
)

type MacOSKeychainStatusError struct {
	KeychainOperation string
	KeychainStatus    int32
}

func (e MacOSKeychainStatusError) Error() string {
	return fmt.Sprintf("macOS Keychain %s failed (OSStatus %d)", e.KeychainOperation, e.KeychainStatus)
}

func macOSKeychainError(keychainOperation string, keychainStatus int32) error {
	switch keychainStatus {
	case macOSKeychainStatusSuccess:
		return nil
	case macOSKeychainStatusItemNotFound:
		return ErrMacOSKeychainItemNotFound
	case macOSKeychainStatusMissingEntitlement:
		return fmt.Errorf("%w: %w", ErrMacOSKeychainNeedsSignedApp, MacOSKeychainStatusError{keychainOperation, keychainStatus})
	default:
		return MacOSKeychainStatusError{keychainOperation, keychainStatus}
	}
}
