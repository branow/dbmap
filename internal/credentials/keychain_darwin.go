//go:build darwin

package credentials

import (
	"fmt"
	"unsafe"
)

// OSStatus codes from SecBase.h, named here because nothing is compiled against
// the SDK: they are part of the framework's ABI, not of a header.
const (
	statusSuccess       = int32(0)
	statusAllocate      = int32(-108)
	statusCanceled      = int32(-128)
	statusNotAvailable  = int32(-25291)
	statusAuthFailed    = int32(-25293)
	statusDuplicate     = int32(-25299)
	statusNotFound      = int32(-25300)
	statusNoInteraction = int32(-25308)
)

// sentinels: the whole authorization family maps to errBlocked because one
// remedy answers all of it, however the refusal was spelled.
var sentinels = map[int32]error{
	statusNotFound:      errMissing,
	statusAuthFailed:    errBlocked,
	statusNoInteraction: errBlocked,
	statusCanceled:      errBlocked,
}

// statuses names the codes dbmap has words for. Anything unlisted still fails:
// an unrecognised refusal is a refusal.
var statuses = map[int32]string{
	statusNotAvailable: "no keychain is available",
	statusDuplicate:    "the item already exists",
	statusAllocate:     "the keychain could not allocate memory",
}

// itemGet reads one item. The item's access control names this binary and that
// identity changes with every build, so allow decides whether the keychain may
// ask about the difference or must refuse the read.
func itemGet(service, account string, allow ui) (string, error) {
	if err := loadSecurity(); err != nil {
		return "", err
	}
	read := itemQuery(service, account)
	defer cfRelease(read)
	cfDictionarySetValue(read, kSecReturnData, kCFBooleanTrue)
	cfDictionarySetValue(read, kSecMatchLimit, kSecMatchLimitOne)
	refuseDialog(read, allow)
	defer holdDialog(allow)()

	var found cfRef
	if status := secItemCopyMatching(read, &found); status != statusSuccess {
		return "", statusError(status)
	}
	defer cfRelease(found)
	return string(cfBytes(found)), nil
}

// itemSet adds the item, or replaces an existing one's value, so a repeated Set
// is idempotent rather than a duplicate-item failure.
func itemSet(service, account, secret string, allow ui) error {
	if err := loadSecurity(); err != nil {
		return err
	}
	value := []byte(secret)
	data := cfDataCreate(0, value, len(value))
	defer cfRelease(data)
	defer holdDialog(allow)()

	add := itemQuery(service, account)
	defer cfRelease(add)
	cfDictionarySetValue(add, kSecValueData, data)
	cfDictionarySetValue(add, kSecAttrAccessible, kSecAttrAccessibleWhenUnlocked)
	refuseDialog(add, allow)

	status := secItemAdd(add, nil)
	if status == statusDuplicate {
		// Replacing an item an earlier build stored is a write the keychain
		// asks about, so this query is silenced too.
		where := itemQuery(service, account)
		defer cfRelease(where)
		refuseDialog(where, allow)
		update := newDict(
			pair{kSecValueData, data},
			pair{kSecAttrAccessible, kSecAttrAccessibleWhenUnlocked},
		)
		defer cfRelease(update)
		status = secItemUpdate(where, update)
	}
	if status != statusSuccess {
		return statusError(status)
	}
	return nil
}

func itemDelete(service, account string, allow ui) error {
	if err := loadSecurity(); err != nil {
		return err
	}
	where := itemQuery(service, account)
	defer cfRelease(where)
	refuseDialog(where, allow)
	defer holdDialog(allow)()

	if status := secItemDelete(where); status != statusSuccess {
		return statusError(status)
	}
	return nil
}

// statusError wraps a sentinel so the number survives into the message.
func statusError(code int32) error {
	if sentinel, ok := sentinels[code]; ok {
		return fmt.Errorf("%w (OSStatus %d)", sentinel, code)
	}
	if reason, ok := statuses[code]; ok {
		return fmt.Errorf("%s (OSStatus %d)", reason, code)
	}
	return fmt.Errorf("the keychain refused the request (OSStatus %d)", code)
}

// itemQuery identifies one dbmap item: a generic password under our service and
// the caller's account. Synchronizable is pinned false so a database password
// never leaves the machine for iCloud. The caller releases the dictionary.
func itemQuery(service, account string) cfRef {
	svc, acct := cfString(service), cfString(account)
	defer cfRelease(svc)
	defer cfRelease(acct)
	return newDict(
		pair{kSecClass, kSecClassGenericPassword},
		pair{kSecAttrService, svc},
		pair{kSecAttrAccount, acct},
		pair{kSecAttrSynchronizable, kCFBooleanFalse},
	)
}

// refuseDialog and holdDialog are the two halves of silencing the keychain, and
// both are needed: with the dictionary key alone, reading an item an earlier
// build stored still blocks on an unseen dialog; with the process switch it
// fails at once.
func refuseDialog(query cfRef, allow ui) {
	if allow {
		return
	}
	cfDictionarySetValue(query, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail)
}

// holdDialog flips the process-wide switch and returns the call that puts it
// back, because the next caller may be one a human is watching.
func holdDialog(allow ui) func() {
	if allow {
		return func() {}
	}
	secAllowInteraction(false)
	return func() { secAllowInteraction(true) }
}

// pair is one entry of a query dictionary. The key is always a framework
// global, so a query reads as the attributes it names.
type pair struct {
	key, value cfRef
}

// newDict builds a mutable dictionary holding the given pairs. The dictionary
// retains each value, so a reference created only to be stored here is released
// by whoever created it.
func newDict(pairs ...pair) cfRef {
	dict := cfDictionaryCreate(0, len(pairs),
		kCFTypeDictionaryKeyCallBacks, kCFTypeDictionaryValueCallBacks)
	for _, p := range pairs {
		cfDictionarySetValue(dict, p.key, p.value)
	}
	return dict
}

// cfString creates a CFString the caller releases.
func cfString(value string) cfRef {
	return cfStringCreate(0, value, kCFStringEncodingUTF8)
}

// cfBytes copies a CFData's bytes into Go memory, which has to happen before
// the reference is released.
func cfBytes(data cfRef) []byte {
	length := cfDataGetLength(data)
	if length == 0 {
		return nil
	}
	return append([]byte(nil), unsafe.Slice(cfDataGetBytePtr(data), length)...)
}
