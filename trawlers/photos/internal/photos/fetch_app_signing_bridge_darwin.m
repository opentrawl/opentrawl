#import <CoreFoundation/CoreFoundation.h>
#import <Security/Security.h>
#include <stdlib.h>
#include <string.h>

int photoscrawl_copy_leaf_certificate(const char *path, unsigned char **bytesOut, long *lengthOut, int *statusOut) {
    *bytesOut = NULL;
    *lengthOut = 0;
    *statusOut = 0;

    CFURLRef url = CFURLCreateFromFileSystemRepresentation(
        kCFAllocatorDefault,
        (const UInt8 *)path,
        (CFIndex)strlen(path),
        0
    );
    if (url == NULL) {
        *statusOut = -1;
        return 0;
    }

    SecStaticCodeRef code = NULL;
    OSStatus status = SecStaticCodeCreateWithPath(url, kSecCSDefaultFlags, &code);
    CFRelease(url);
    if (status != errSecSuccess) {
        *statusOut = (int)status;
        return 0;
    }

    CFDictionaryRef information = NULL;
    status = SecCodeCopySigningInformation(code, kSecCSSigningInformation, &information);
    CFRelease(code);
    if (status != errSecSuccess) {
        *statusOut = (int)status;
        return 0;
    }

    CFArrayRef certificates = (CFArrayRef)CFDictionaryGetValue(information, kSecCodeInfoCertificates);
    if (certificates == NULL || CFArrayGetCount(certificates) == 0) {
        CFRelease(information);
        *statusOut = -2;
        return 0;
    }

    SecCertificateRef leaf = (SecCertificateRef)CFArrayGetValueAtIndex(certificates, 0);
    CFDataRef data = SecCertificateCopyData(leaf);
    CFRelease(information);
    if (data == NULL || CFDataGetLength(data) <= 0) {
        if (data != NULL) {
            CFRelease(data);
        }
        *statusOut = -3;
        return 0;
    }

    CFIndex length = CFDataGetLength(data);
    unsigned char *copy = malloc((size_t)length);
    if (copy == NULL) {
        CFRelease(data);
        *statusOut = -4;
        return 0;
    }
    memcpy(copy, CFDataGetBytePtr(data), (size_t)length);
    CFRelease(data);

    *bytesOut = copy;
    *lengthOut = (long)length;
    return 1;
}
