#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#import <Security/Security.h>
#include <stdlib.h>
#include <string.h>

#include "macos_data_protection_keychain_native.h"

static PluginKeychainBytesResult pluginKeychainBytesResultFromData(OSStatus status, CFDataRef data) {
  PluginKeychainBytesResult result = {status, NULL, 0};
  if (status != errSecSuccess || data == NULL) {
    return result;
  }
  CFIndex length = CFDataGetLength(data);
  result.returned_bytes = malloc(length > 0 ? (size_t)length : 1);
  if (result.returned_bytes == NULL) {
    result.keychain_status = errSecAllocate;
    return result;
  }
  if (length > 0) {
    memcpy(result.returned_bytes, CFDataGetBytePtr(data), (size_t)length);
  }
  result.returned_byte_count = (size_t)length;
  return result;
}

static LAContext *newPluginKeychainNonInteractiveContext(void) {
  LAContext *context = [[LAContext alloc] init];
  context.interactionNotAllowed = YES;
  return context;
}

static CFMutableDictionaryRef newPluginGenericPasswordQuery(const char *service, const char *account, LAContext *context) {
  CFMutableDictionaryRef query = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  CFStringRef serviceString = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
  CFStringRef accountString = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
  CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
  CFDictionarySetValue(query, kSecUseDataProtectionKeychain, kCFBooleanTrue);
  CFDictionarySetValue(query, kSecAttrSynchronizable, kCFBooleanFalse);
  CFDictionarySetValue(query, kSecUseAuthenticationContext, (__bridge CFTypeRef)context);
  CFDictionarySetValue(query, kSecAttrService, serviceString);
  CFDictionarySetValue(query, kSecAttrAccount, accountString);
  CFRelease(serviceString);
  CFRelease(accountString);
  return query;
}

int32_t savePluginSecretInDataProtectionKeychain(const char *service, const char *account, const uint8_t *secret_bytes, size_t secret_byte_count) {
  @autoreleasepool {
    LAContext *context = newPluginKeychainNonInteractiveContext();
    CFMutableDictionaryRef query = newPluginGenericPasswordQuery(service, account, context);
    CFDataRef secretData = CFDataCreate(kCFAllocatorDefault, secret_bytes, (CFIndex)secret_byte_count);
    CFMutableDictionaryRef update = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(update, kSecValueData, secretData);
    OSStatus status = SecItemUpdate(query, update);
    if (status == errSecItemNotFound) {
      CFDictionarySetValue(query, kSecAttrAccessible, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);
      CFDictionarySetValue(query, kSecValueData, secretData);
      status = SecItemAdd(query, NULL);
    }
    CFRelease(update);
    CFRelease(secretData);
    CFRelease(query);
    [context release];
    return status;
  }
}

PluginKeychainBytesResult loadPluginSecretFromDataProtectionKeychain(const char *service, const char *account) {
  @autoreleasepool {
    LAContext *context = newPluginKeychainNonInteractiveContext();
    CFMutableDictionaryRef query = newPluginGenericPasswordQuery(service, account, context);
    CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
    CFTypeRef returned = NULL;
    OSStatus status = SecItemCopyMatching(query, &returned);
    PluginKeychainBytesResult result = pluginKeychainBytesResultFromData(status, (CFDataRef)returned);
    if (returned != NULL) {
      CFRelease(returned);
    }
    CFRelease(query);
    [context release];
    return result;
  }
}

int32_t deletePluginSecretFromDataProtectionKeychain(const char *service, const char *account) {
  @autoreleasepool {
    LAContext *context = newPluginKeychainNonInteractiveContext();
    CFMutableDictionaryRef query = newPluginGenericPasswordQuery(service, account, context);
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    [context release];
    return status;
  }
}

void freePluginKeychainBytes(uint8_t *returned_bytes) {
  free(returned_bytes);
}
