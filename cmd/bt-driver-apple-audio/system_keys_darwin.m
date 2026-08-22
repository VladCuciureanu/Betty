//go:build darwin

#import <Security/Security.h>
#include <stdlib.h>
#include <string.h>
#include "system_keys_darwin.h"

int betty_airpods_system_keys(const char *account, void **output, size_t *length) {
    if (account == NULL || output == NULL || length == NULL) return errSecParam;
    *output = NULL;
    *length = 0;

    SecKeychainRef keychain = NULL;
    OSStatus status = SecKeychainOpen("/Library/Keychains/System.keychain", &keychain);
    if (status != errSecSuccess) return (int)status;

    UInt32 passwordLength = 0;
    void *passwordData = NULL;
    const char *service = "MobileBluetooth";
    status = SecKeychainFindGenericPassword(
        keychain,
        (UInt32)strlen(service), service,
        (UInt32)strlen(account), account,
        &passwordLength, &passwordData, NULL
    );
    CFRelease(keychain);
    if (status != errSecSuccess) return (int)status;

    void *copy = malloc(passwordLength);
    if (copy == NULL) {
        SecKeychainItemFreeContent(NULL, passwordData);
        return errSecAllocate;
    }
    memcpy(copy, passwordData, passwordLength);
    SecKeychainItemFreeContent(NULL, passwordData);
    *output = copy;
    *length = (size_t)passwordLength;
    return errSecSuccess;
}
