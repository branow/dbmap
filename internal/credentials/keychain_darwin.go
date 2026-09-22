//go:build darwin && cgo

package credentials

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// query builds the dictionary that identifies one dbmap item: a generic
// password filed under our service and the caller's account. Synchronizable is
// pinned false so a database password never leaves the machine for iCloud.
static CFDictionaryRef dbmapQuery(const char *service, const char *account) {
	CFStringRef svc = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef acct = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	const void *keys[] = {
		kSecClass, kSecAttrService, kSecAttrAccount, kSecAttrSynchronizable,
	};
	const void *values[] = {
		kSecClassGenericPassword, svc, acct, kCFBooleanFalse,
	};
	CFDictionaryRef query = CFDictionaryCreate(NULL, keys, values, 4,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFRelease(svc);
	CFRelease(acct);
	return query;
}

// dbmapSet adds the item, or replaces the value of one that already exists, so
// a repeated Set is idempotent rather than a duplicate-item failure.
static OSStatus dbmapSet(const char *service, const char *account,
                         const void *secret, int length) {
	CFDictionaryRef query = dbmapQuery(service, account);
	CFDataRef data = CFDataCreate(NULL, (const UInt8 *)secret, (CFIndex)length);

	CFMutableDictionaryRef add = CFDictionaryCreateMutableCopy(NULL, 0, query);
	CFDictionarySetValue(add, kSecValueData, data);
	CFDictionarySetValue(add, kSecAttrAccessible, kSecAttrAccessibleWhenUnlocked);
	OSStatus status = SecItemAdd(add, NULL);
	CFRelease(add);

	if (status == errSecDuplicateItem) {
		CFMutableDictionaryRef update = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		CFDictionarySetValue(update, kSecValueData, data);
		CFDictionarySetValue(update, kSecAttrAccessible, kSecAttrAccessibleWhenUnlocked);
		status = SecItemUpdate(query, update);
		CFRelease(update);
	}

	CFRelease(data);
	CFRelease(query);
	return status;
}

// dbmapGet copies the item's value into a buffer the caller frees.
static OSStatus dbmapGet(const char *service, const char *account,
                         void **out, int *length) {
	CFDictionaryRef query = dbmapQuery(service, account);
	CFMutableDictionaryRef read = CFDictionaryCreateMutableCopy(NULL, 0, query);
	CFDictionarySetValue(read, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(read, kSecMatchLimit, kSecMatchLimitOne);

	CFTypeRef found = NULL;
	OSStatus status = SecItemCopyMatching(read, &found);
	if (status == errSecSuccess && found != NULL) {
		CFDataRef data = (CFDataRef)found;
		CFIndex size = CFDataGetLength(data);
		void *buffer = malloc(size > 0 ? (size_t)size : 1);
		if (buffer == NULL) {
			status = errSecAllocate;
		} else {
			if (size > 0) {
				CFDataGetBytes(data, CFRangeMake(0, size), (UInt8 *)buffer);
			}
			*out = buffer;
			*length = (int)size;
		}
	}
	if (found != NULL) {
		CFRelease(found);
	}
	CFRelease(read);
	CFRelease(query);
	return status;
}

static OSStatus dbmapDelete(const char *service, const char *account) {
	CFDictionaryRef query = dbmapQuery(service, account);
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// statuses names the OSStatus codes dbmap acts on. It is a table so a new code
// is a new row; anything not listed still fails, because an unrecognised
// refusal from the keychain is a refusal, and dbmap never answers one by
// writing the secret somewhere weaker.
var statuses = map[int32]string{
	int32(C.errSecAuthFailed):            "the keychain refused authorization",
	int32(C.errSecInteractionNotAllowed): "the keychain cannot ask for authorization here",
	int32(C.errSecUserCanceled):          "the authorization request was dismissed",
	int32(C.errSecNotAvailable):          "no keychain is available",
	int32(C.errSecDuplicateItem):         "the item already exists",
	int32(C.errSecAllocate):              "the keychain could not allocate memory",
}

// itemGet reads one item through the Security framework. Because dbmap calls it
// in its own process, the item's access control names this binary rather than a
// command line tool it shelled out to.
func itemGet(service, account string) (string, error) {
	cService, cAccount, release := strings2(service, account)
	defer release()

	var buffer unsafe.Pointer
	var length C.int
	status := C.dbmapGet(cService, cAccount, &buffer, &length)
	if status != C.errSecSuccess {
		return "", statusError(status)
	}
	defer C.free(buffer)
	return string(C.GoBytes(buffer, length)), nil
}

func itemSet(service, account, secret string) error {
	cService, cAccount, release := strings2(service, account)
	defer release()

	value := []byte(secret)
	var head unsafe.Pointer
	if len(value) > 0 {
		head = unsafe.Pointer(&value[0])
	}
	status := C.dbmapSet(cService, cAccount, head, C.int(len(value)))
	if status != C.errSecSuccess {
		return statusError(status)
	}
	return nil
}

func itemDelete(service, account string) error {
	cService, cAccount, release := strings2(service, account)
	defer release()

	if status := C.dbmapDelete(cService, cAccount); status != C.errSecSuccess {
		return statusError(status)
	}
	return nil
}

// statusError translates an OSStatus. A missing item is the one status that is
// not a failure of the keychain, so it becomes the shared sentinel.
func statusError(status C.OSStatus) error {
	if status == C.errSecItemNotFound {
		return errMissing
	}
	code := int32(status)
	if reason, ok := statuses[code]; ok {
		return fmt.Errorf("%s (OSStatus %d)", reason, code)
	}
	return fmt.Errorf("the keychain refused the request (OSStatus %d)", code)
}

// strings2 converts both C strings at once, because every call needs the same
// pair and the same freeing.
func strings2(a, b string) (*C.char, *C.char, func()) {
	first, second := C.CString(a), C.CString(b)
	return first, second, func() {
		C.free(unsafe.Pointer(first))
		C.free(unsafe.Pointer(second))
	}
}
