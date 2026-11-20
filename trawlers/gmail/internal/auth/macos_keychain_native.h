#include <stddef.h>
#include <stdint.h>

typedef struct {
  int32_t keychain_status;
  uint8_t *returned_bytes;
  size_t returned_byte_count;
} MacOSKeychainBytesResult;

int32_t saveGenericPasswordInDataProtectionKeychain(const char *service, const char *account, const uint8_t *password_bytes, size_t password_byte_count);
MacOSKeychainBytesResult loadGenericPasswordFromDataProtectionKeychain(const char *service, const char *account);
int32_t deleteGenericPasswordFromDataProtectionKeychain(const char *service, const char *account);

int32_t createSecureEnclaveSigningKey(const char *application_tag);
MacOSKeychainBytesResult copySecureEnclaveSigningKeyPublicKey(const char *application_tag);
MacOSKeychainBytesResult signWithSecureEnclaveSigningKey(const char *application_tag, const uint8_t *message_bytes, size_t message_byte_count);
int32_t deleteSecureEnclaveSigningKey(const char *application_tag);

void freeMacOSKeychainBytes(uint8_t *returned_bytes);
