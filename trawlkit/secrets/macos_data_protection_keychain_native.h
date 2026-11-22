#include <stddef.h>
#include <stdint.h>

typedef struct {
  int32_t keychain_status;
  uint8_t *returned_bytes;
  size_t returned_byte_count;
} PluginKeychainBytesResult;

int32_t savePluginSecretInDataProtectionKeychain(const char *service, const char *account, const uint8_t *secret_bytes, size_t secret_byte_count);
PluginKeychainBytesResult loadPluginSecretFromDataProtectionKeychain(const char *service, const char *account);
int32_t deletePluginSecretFromDataProtectionKeychain(const char *service, const char *account);
void freePluginKeychainBytes(uint8_t *returned_bytes);
