//go:build darwin && cgo

package credentials

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// SecKeychainSetUserInteractionAllowed is declared here because recent SDKs
// dropped the declaration from SecKeychain.h while Security.framework still
// exports it. Nothing else silences the login keychain: kSecUseAuthenticationUI
// governs the data protection keychain, and dbmap's items live in the login
// keychain, whose access control dialog is the one that blocks forever.
extern OSStatus SecKeychainSetUserInteractionAllowed(Boolean state);

// kSecUseAuthenticationUIFail is deprecated in favour of an LAContext, which
// speaks for the data protection keychain only and would drag in
// LocalAuthentication and Objective-C for a case the process switch above
// already covers. The key still carries its documented meaning, so the
// deprecation warning is silenced rather than followed.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

// dbmapSilence refuses the authorization dialog for the call about to be made.
// Both halves are needed, and they were measured on macOS 15.7: with only the
// dictionary key, a read of an item stored by an earlier build still blocks on
// an unseen dialog; with the process switch it returns errSecAuthFailed at
// once. The switch is process wide, so dbmapRestore must follow. Silencing
// twice within one operation is harmless.
static void dbmapSilence(CFMutableDictionaryRef query, int allowUI) {
	if (allowUI) {
		return;
	}
	CFDictionarySetValue(query, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	SecKeychainSetUserInteractionAllowed(FALSE);
}

// dbmapRestore puts the process switch back, because the next call may be one a
// human is watching.
static void dbmapRestore(int allowUI) {
	if (!allowUI) {
		SecKeychainSetUserInteractionAllowed(TRUE);
	}
}

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
                         const void *secret, int length, int allowUI) {
	CFDictionaryRef query = dbmapQuery(service, account);
	CFDataRef data = CFDataCreate(NULL, (const UInt8 *)secret, (CFIndex)length);

	CFMutableDictionaryRef add = CFDictionaryCreateMutableCopy(NULL, 0, query);
	CFDictionarySetValue(add, kSecValueData, data);
	CFDictionarySetValue(add, kSecAttrAccessible, kSecAttrAccessibleWhenUnlocked);
	dbmapSilence(add, allowUI);
	OSStatus status = SecItemAdd(add, NULL);
	CFRelease(add);

	if (status == errSecDuplicateItem) {
		// Replacing the value of an item an earlier build stored is a write the
		// keychain asks about, so this query is silenced too.
		CFMutableDictionaryRef where = CFDictionaryCreateMutableCopy(NULL, 0, query);
		dbmapSilence(where, allowUI);
		CFMutableDictionaryRef update = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		CFDictionarySetValue(update, kSecValueData, data);
		CFDictionarySetValue(update, kSecAttrAccessible, kSecAttrAccessibleWhenUnlocked);
		status = SecItemUpdate(where, update);
		CFRelease(update);
		CFRelease(where);
	}

	dbmapRestore(allowUI);
	CFRelease(data);
	CFRelease(query);
	return status;
}

// dbmapGet copies the item's value into a buffer the caller frees.
static OSStatus dbmapGet(const char *service, const char *account,
                         int allowUI, void **out, int *length) {
	CFDictionaryRef query = dbmapQuery(service, account);
	CFMutableDictionaryRef read = CFDictionaryCreateMutableCopy(NULL, 0, query);
	CFDictionarySetValue(read, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(read, kSecMatchLimit, kSecMatchLimitOne);
	dbmapSilence(read, allowUI);

	CFTypeRef found = NULL;
	OSStatus status = SecItemCopyMatching(read, &found);
	dbmapRestore(allowUI);
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

static OSStatus dbmapDelete(const char *service, const char *account, int allowUI) {
	CFDictionaryRef query = dbmapQuery(service, account);
	CFMutableDictionaryRef where = CFDictionaryCreateMutableCopy(NULL, 0, query);
	dbmapSilence(where, allowUI);
	OSStatus status = SecItemDelete(where);
	dbmapRestore(allowUI);
	CFRelease(where);
	CFRelease(query);
	return status;
}

#pragma clang diagnostic pop
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// The OSStatus codes dbmap names. They are Go constants because cgo cannot be
// used from a _test.go file, and the table below is worth a test of its own.
const (
	statusNotFound      = int32(C.errSecItemNotFound)
	statusAuthFailed    = int32(C.errSecAuthFailed)
	statusNoInteraction = int32(C.errSecInteractionNotAllowed)
	statusCanceled      = int32(C.errSecUserCanceled)
	statusNotAvailable  = int32(C.errSecNotAvailable)
	statusDuplicate     = int32(C.errSecDuplicateItem)
	statusAllocate      = int32(C.errSecAllocate)
)

// sentinels names the OSStatus codes the store above reasons about rather than
// only reports: the item is absent, or the keychain would hand it over only
// after the user answered something. The whole authorization family maps to one
// sentinel because one remedy answers all of it - the login keychain reports a
// refused dialog as errSecAuthFailed and the data protection keychain as
// errSecInteractionNotAllowed, and a dismissed dialog leaves the user in the
// same place as a refused one.
var sentinels = map[int32]error{
	statusNotFound:      errMissing,
	statusAuthFailed:    errBlocked,
	statusNoInteraction: errBlocked,
	statusCanceled:      errBlocked,
}

// statuses names the remaining OSStatus codes dbmap has words for. It is a
// table so a new code is a new row; anything not listed still fails, because an
// unrecognised refusal from the keychain is a refusal, and dbmap never answers
// one by writing the secret somewhere weaker.
var statuses = map[int32]string{
	statusNotAvailable: "no keychain is available",
	statusDuplicate:    "the item already exists",
	statusAllocate:     "the keychain could not allocate memory",
}

// itemGet reads one item through the Security framework. Because dbmap calls it
// in its own process, the item's access control names this binary rather than a
// command line tool it shelled out to. That identity changes with every build,
// so allow decides whether the keychain may ask the user about the difference
// or must refuse the read instead.
func itemGet(service, account string, allow ui) (string, error) {
	cService, cAccount, release := strings2(service, account)
	defer release()

	var buffer unsafe.Pointer
	var length C.int
	status := C.dbmapGet(cService, cAccount, flag(allow), &buffer, &length)
	if status != C.errSecSuccess {
		return "", statusError(int32(status))
	}
	defer C.free(buffer)
	return string(C.GoBytes(buffer, length)), nil
}

func itemSet(service, account, secret string, allow ui) error {
	cService, cAccount, release := strings2(service, account)
	defer release()

	value := []byte(secret)
	var head unsafe.Pointer
	if len(value) > 0 {
		head = unsafe.Pointer(&value[0])
	}
	status := C.dbmapSet(cService, cAccount, head, C.int(len(value)), flag(allow))
	if status != C.errSecSuccess {
		return statusError(int32(status))
	}
	return nil
}

func itemDelete(service, account string, allow ui) error {
	cService, cAccount, release := strings2(service, account)
	defer release()

	if status := C.dbmapDelete(cService, cAccount, flag(allow)); status != C.errSecSuccess {
		return statusError(int32(status))
	}
	return nil
}

// statusError translates an OSStatus. A code the store above acts on keeps its
// sentinel, wrapped so the number survives into the message.
func statusError(code int32) error {
	if sentinel, ok := sentinels[code]; ok {
		return fmt.Errorf("%w (OSStatus %d)", sentinel, code)
	}
	if reason, ok := statuses[code]; ok {
		return fmt.Errorf("%s (OSStatus %d)", reason, code)
	}
	return fmt.Errorf("the keychain refused the request (OSStatus %d)", code)
}

// flag converts the decision to the C int the Security calls take, because cgo
// will not widen a Go bool for us.
func flag(allow ui) C.int {
	if allow {
		return 1
	}
	return 0
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
