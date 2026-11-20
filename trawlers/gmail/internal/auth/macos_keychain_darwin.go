//go:build darwin

package auth

/*
#cgo LDFLAGS: -framework Foundation -framework Security -framework LocalAuthentication
#include <stdlib.h>
#include "macos_keychain_native.h"
*/
import "C"

import (
	"unsafe"
)

func saveGenericPasswordInDataProtectionKeychain(service, account string, password []byte) error {
	serviceText, accountText := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(serviceText))
	defer C.free(unsafe.Pointer(accountText))
	status := C.saveGenericPasswordInDataProtectionKeychain(serviceText, accountText, cBytes(password), C.size_t(len(password)))
	return macOSKeychainError("save", int32(status))
}

func loadGenericPasswordFromDataProtectionKeychain(service, account string) ([]byte, error) {
	serviceText, accountText := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(serviceText))
	defer C.free(unsafe.Pointer(accountText))
	return bytesFromMacOSKeychainResult("load", C.loadGenericPasswordFromDataProtectionKeychain(serviceText, accountText))
}

func deleteGenericPasswordFromDataProtectionKeychain(service, account string) error {
	serviceText, accountText := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(serviceText))
	defer C.free(unsafe.Pointer(accountText))
	return macOSKeychainError("delete", int32(C.deleteGenericPasswordFromDataProtectionKeychain(serviceText, accountText)))
}

func createSecureEnclaveSigningKey(applicationTag string) error {
	tagText := C.CString(applicationTag)
	defer C.free(unsafe.Pointer(tagText))
	return macOSKeychainError("Secure Enclave key create", int32(C.createSecureEnclaveSigningKey(tagText)))
}

func copySecureEnclaveSigningKeyPublicKey(applicationTag string) ([]byte, error) {
	tagText := C.CString(applicationTag)
	defer C.free(unsafe.Pointer(tagText))
	return bytesFromMacOSKeychainResult("Secure Enclave public key", C.copySecureEnclaveSigningKeyPublicKey(tagText))
}

func signWithSecureEnclaveSigningKey(applicationTag string, message []byte) ([]byte, error) {
	tagText := C.CString(applicationTag)
	defer C.free(unsafe.Pointer(tagText))
	return bytesFromMacOSKeychainResult("Secure Enclave sign", C.signWithSecureEnclaveSigningKey(tagText, cBytes(message), C.size_t(len(message))))
}

func deleteSecureEnclaveSigningKey(applicationTag string) error {
	tagText := C.CString(applicationTag)
	defer C.free(unsafe.Pointer(tagText))
	return macOSKeychainError("Secure Enclave key delete", int32(C.deleteSecureEnclaveSigningKey(tagText)))
}

func cBytes(value []byte) *C.uint8_t {
	if len(value) == 0 {
		return nil
	}
	return (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(value)))
}

func bytesFromMacOSKeychainResult(operation string, result C.MacOSKeychainBytesResult) ([]byte, error) {
	defer C.freeMacOSKeychainBytes(result.returned_bytes)
	if err := macOSKeychainError(operation, int32(result.keychain_status)); err != nil {
		return nil, err
	}
	return C.GoBytes(unsafe.Pointer(result.returned_bytes), C.int(result.returned_byte_count)), nil
}
