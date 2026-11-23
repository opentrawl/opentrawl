#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#import <Security/Security.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

#include "macos_keychain_native.h"

// Every query uses the non-synchronising Data Protection Keychain, the
// signed app's default access group, and an authentication context that
// fails instead of showing a prompt.

static MacOSKeychainBytesResult keychainBytesResultFromData(OSStatus status, CFDataRef data) {
  MacOSKeychainBytesResult result = {status, NULL, 0};
  if (status != errSecSuccess || data == NULL) {
    return result;
  }
  CFIndex length = CFDataGetLength(data);
  result.returned_bytes = malloc(length > 0 ? (size_t)length : 1);
  if (result.returned_bytes == NULL) {
    result.keychain_status = errSecAllocate;
    return result;
  }
  memcpy(result.returned_bytes, CFDataGetBytePtr(data), (size_t)length);
  result.returned_byte_count = (size_t)length;
  return result;
}

static CFMutableDictionaryRef newDataProtectionKeychainQuery(CFStringRef itemClass, LAContext *context) {
  CFMutableDictionaryRef query = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  CFDictionarySetValue(query, kSecClass, itemClass);
  CFDictionarySetValue(query, kSecUseDataProtectionKeychain, kCFBooleanTrue);
  CFDictionarySetValue(query, kSecAttrSynchronizable, kCFBooleanFalse);
  CFDictionarySetValue(query, kSecUseAuthenticationContext, (__bridge CFTypeRef)context);
  return query;
}

static LAContext *newNonInteractiveAuthenticationContext(void) {
  LAContext *context = [[LAContext alloc] init];
  context.interactionNotAllowed = YES;
  return context;
}

static CFMutableDictionaryRef newGenericPasswordQuery(const char *service, const char *account, LAContext *context) {
  CFMutableDictionaryRef query = newDataProtectionKeychainQuery(kSecClassGenericPassword, context);
  CFStringRef serviceString = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
  CFStringRef accountString = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
  CFDictionarySetValue(query, kSecAttrService, serviceString);
  CFDictionarySetValue(query, kSecAttrAccount, accountString);
  CFRelease(serviceString);
  CFRelease(accountString);
  return query;
}

int32_t saveGenericPasswordInDataProtectionKeychain(const char *service, const char *account, const uint8_t *password_bytes, size_t password_byte_count) {
  @autoreleasepool {
    LAContext *context = newNonInteractiveAuthenticationContext();
    CFMutableDictionaryRef query = newGenericPasswordQuery(service, account, context);
    CFDataRef passwordData = CFDataCreate(kCFAllocatorDefault, password_bytes, (CFIndex)password_byte_count);
    CFMutableDictionaryRef update = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(update, kSecValueData, passwordData);
    OSStatus status = SecItemUpdate(query, update);
    if (status == errSecItemNotFound) {
      CFDictionarySetValue(query, kSecAttrAccessible, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);
      CFDictionarySetValue(query, kSecValueData, passwordData);
      status = SecItemAdd(query, NULL);
    }
    CFRelease(update);
    CFRelease(passwordData);
    CFRelease(query);
    [context release];
    return status;
  }
}

static bool currentProcessHasDataProtectionKeychainEntitlement(void) {
  SecTaskRef task = SecTaskCreateFromSelf(kCFAllocatorDefault);
  if (task == NULL) {
    return false;
  }
  CFTypeRef appIdentifier = SecTaskCopyValueForEntitlement(task, CFSTR("com.apple.application-identifier"), NULL);
  CFTypeRef accessGroups = SecTaskCopyValueForEntitlement(task, CFSTR("keychain-access-groups"), NULL);
  bool hasEntitlement = (appIdentifier != NULL) || (accessGroups != NULL);
  if (appIdentifier != NULL) {
    CFRelease(appIdentifier);
  }
  if (accessGroups != NULL) {
    CFRelease(accessGroups);
  }
  CFRelease(task);
  return hasEntitlement;
}

MacOSKeychainBytesResult loadGenericPasswordFromDataProtectionKeychain(const char *service, const char *account) {
  @autoreleasepool {
    LAContext *context = newNonInteractiveAuthenticationContext();
    CFMutableDictionaryRef query = newGenericPasswordQuery(service, account, context);
    CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
    CFTypeRef returned = NULL;
    OSStatus status = SecItemCopyMatching(query, &returned);
    if (status == errSecItemNotFound && !currentProcessHasDataProtectionKeychainEntitlement()) {
      status = errSecMissingEntitlement;
    }
    MacOSKeychainBytesResult result = keychainBytesResultFromData(status, (CFDataRef)returned);
    if (returned != NULL) {
      CFRelease(returned);
    }
    CFRelease(query);
    [context release];
    return result;
  }
}

int32_t deleteGenericPasswordFromDataProtectionKeychain(const char *service, const char *account) {
  @autoreleasepool {
    LAContext *context = newNonInteractiveAuthenticationContext();
    CFMutableDictionaryRef query = newGenericPasswordQuery(service, account, context);
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    [context release];
    return status;
  }
}

static CFDataRef newApplicationTagData(const char *application_tag) {
  return CFDataCreate(kCFAllocatorDefault, (const UInt8 *)application_tag, (CFIndex)strlen(application_tag));
}

static CFMutableDictionaryRef newSecureEnclaveKeyQuery(const char *application_tag, LAContext *context) {
  CFMutableDictionaryRef query = newDataProtectionKeychainQuery(kSecClassKey, context);
  CFDataRef tagData = newApplicationTagData(application_tag);
  CFDictionarySetValue(query, kSecAttrKeyClass, kSecAttrKeyClassPrivate);
  CFDictionarySetValue(query, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
  CFDictionarySetValue(query, kSecAttrApplicationTag, tagData);
  CFRelease(tagData);
  return query;
}

static OSStatus copySecureEnclavePrivateKey(const char *application_tag, SecKeyRef *privateKey) {
  LAContext *context = newNonInteractiveAuthenticationContext();
  CFMutableDictionaryRef query = newSecureEnclaveKeyQuery(application_tag, context);
  CFDictionarySetValue(query, kSecReturnRef, kCFBooleanTrue);
  CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
  CFTypeRef returned = NULL;
  OSStatus status = SecItemCopyMatching(query, &returned);
  CFRelease(query);
  [context release];
  if (status == errSecSuccess) {
    *privateKey = (SecKeyRef)returned;
  }
  return status;
}

static OSStatus keychainStatusFromError(CFErrorRef error) {
  if (error == NULL) {
    return errSecInternalError;
  }
  OSStatus status = (OSStatus)CFErrorGetCode(error);
  CFRelease(error);
  return status == errSecSuccess ? errSecInternalError : status;
}

int32_t createSecureEnclaveSigningKey(const char *application_tag) {
  @autoreleasepool {
    CFErrorRef error = NULL;
    SecAccessControlRef accessControl = SecAccessControlCreateWithFlags(
      kCFAllocatorDefault, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, kSecAccessControlPrivateKeyUsage, &error);
    if (accessControl == NULL) {
      return keychainStatusFromError(error);
    }
    CFDataRef tagData = newApplicationTagData(application_tag);
    CFMutableDictionaryRef privateKeyAttributes = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(privateKeyAttributes, kSecAttrIsPermanent, kCFBooleanTrue);
    CFDictionarySetValue(privateKeyAttributes, kSecAttrApplicationTag, tagData);
    CFDictionarySetValue(privateKeyAttributes, kSecAttrAccessControl, accessControl);
    CFDictionarySetValue(privateKeyAttributes, kSecAttrSynchronizable, kCFBooleanFalse);
    int keySizeInBits = 256;
    CFNumberRef keySize = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &keySizeInBits);
    CFMutableDictionaryRef attributes = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(attributes, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
    CFDictionarySetValue(attributes, kSecAttrKeySizeInBits, keySize);
    CFDictionarySetValue(attributes, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
    CFDictionarySetValue(attributes, kSecUseDataProtectionKeychain, kCFBooleanTrue);
    CFDictionarySetValue(attributes, kSecPrivateKeyAttrs, privateKeyAttributes);
    SecKeyRef privateKey = SecKeyCreateRandomKey(attributes, &error);
    CFRelease(attributes);
    CFRelease(keySize);
    CFRelease(privateKeyAttributes);
    CFRelease(tagData);
    CFRelease(accessControl);
    if (privateKey == NULL) {
      return keychainStatusFromError(error);
    }
    CFRelease(privateKey);
    return errSecSuccess;
  }
}

MacOSKeychainBytesResult copySecureEnclaveSigningKeyPublicKey(const char *application_tag) {
  @autoreleasepool {
    SecKeyRef privateKey = NULL;
    OSStatus status = copySecureEnclavePrivateKey(application_tag, &privateKey);
    if (status != errSecSuccess) {
      return keychainBytesResultFromData(status, NULL);
    }
    SecKeyRef publicKey = SecKeyCopyPublicKey(privateKey);
    CFRelease(privateKey);
    if (publicKey == NULL) {
      return keychainBytesResultFromData(errSecInternalError, NULL);
    }
    CFErrorRef error = NULL;
    CFDataRef publicKeyData = SecKeyCopyExternalRepresentation(publicKey, &error);
    CFRelease(publicKey);
    if (publicKeyData == NULL) {
      return keychainBytesResultFromData(keychainStatusFromError(error), NULL);
    }
    MacOSKeychainBytesResult result = keychainBytesResultFromData(errSecSuccess, publicKeyData);
    CFRelease(publicKeyData);
    return result;
  }
}

MacOSKeychainBytesResult signWithSecureEnclaveSigningKey(const char *application_tag, const uint8_t *message_bytes, size_t message_byte_count) {
  @autoreleasepool {
    SecKeyRef privateKey = NULL;
    OSStatus status = copySecureEnclavePrivateKey(application_tag, &privateKey);
    if (status != errSecSuccess) {
      return keychainBytesResultFromData(status, NULL);
    }
    SecKeyAlgorithm algorithm = kSecKeyAlgorithmECDSASignatureMessageX962SHA256;
    if (!SecKeyIsAlgorithmSupported(privateKey, kSecKeyOperationTypeSign, algorithm)) {
      CFRelease(privateKey);
      return keychainBytesResultFromData(errSecUnimplemented, NULL);
    }
    CFDataRef message = CFDataCreate(kCFAllocatorDefault, message_bytes, (CFIndex)message_byte_count);
    CFErrorRef error = NULL;
    CFDataRef signature = SecKeyCreateSignature(privateKey, algorithm, message, &error);
    CFRelease(message);
    CFRelease(privateKey);
    if (signature == NULL) {
      return keychainBytesResultFromData(keychainStatusFromError(error), NULL);
    }
    MacOSKeychainBytesResult result = keychainBytesResultFromData(errSecSuccess, signature);
    CFRelease(signature);
    return result;
  }
}

int32_t deleteSecureEnclaveSigningKey(const char *application_tag) {
  @autoreleasepool {
    LAContext *context = newNonInteractiveAuthenticationContext();
    CFMutableDictionaryRef query = newSecureEnclaveKeyQuery(application_tag, context);
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    [context release];
    return status;
  }
}

void freeMacOSKeychainBytes(uint8_t *returned_bytes) {
  free(returned_bytes);
}
