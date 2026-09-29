//go:build darwin

package credentials

// Security.framework is loaded at run time with purego rather than linked with
// cgo. The framework is the same one either way, and its ABI is stable; what
// the runtime load drops is the C toolchain, the macOS SDK and the C link step
// from every build, and with them the rule that a darwin binary can only be
// built on a darwin machine.
//
// purego resolves a symbol by name, so a function recent SDK headers no longer
// declare is reachable again, and nothing here depends on a header at all: the
// OSStatus codes and CoreFoundation constants below are ABI, spelled out once.

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// cfRef is an opaque CoreFoundation object. dbmap only ever hands one back to
// the framework, so it stays a pointer-sized word on the Go side, and retain
// and release discipline is the caller's, exactly as it is in C.
type cfRef uintptr

// The frameworks live in the dyld shared cache, not on disk; dlopen resolves
// these paths anyway.
const (
	securityPath       = "/System/Library/Frameworks/Security.framework/Security"
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
)

// kCFStringEncodingUTF8 is the one encoding dbmap passes: a service and an
// account name are both UTF-8 Go strings.
const kCFStringEncodingUTF8 = uint32(0x08000100)

// The keychain calls. SecKeychainSetUserInteractionAllowed is the process-wide
// switch that silences the login keychain, where dbmap's items live; it is
// absent from recent SDK headers and still exported by the framework, which is
// a problem for a header-driven build and none for this one.
var (
	secItemAdd          func(query cfRef, result *cfRef) int32
	secItemCopyMatching func(query cfRef, result *cfRef) int32
	secItemUpdate       func(query, attributes cfRef) int32
	secItemDelete       func(query cfRef) int32
	secAllowInteraction func(state bool) int32
)

// The CoreFoundation calls needed to build a query dictionary and read a result.
var (
	cfRelease            func(ref cfRef)
	cfStringCreate       func(allocator cfRef, str string, encoding uint32) cfRef
	cfDataCreate         func(allocator cfRef, bytes []byte, length int) cfRef
	cfDataGetLength      func(data cfRef) int
	cfDataGetBytePtr     func(data cfRef) *byte
	cfDictionaryCreate   func(allocator cfRef, capacity int, keys, values uintptr) cfRef
	cfDictionarySetValue func(dict, key, value cfRef)
)

// The framework globals a query names. Each is a CFTypeRef the framework owns
// for the life of the process, so none of them is released.
var (
	kSecClass                      cfRef
	kSecClassGenericPassword       cfRef
	kSecAttrService                cfRef
	kSecAttrAccount                cfRef
	kSecAttrSynchronizable         cfRef
	kSecAttrAccessible             cfRef
	kSecAttrAccessibleWhenUnlocked cfRef
	kSecValueData                  cfRef
	kSecReturnData                 cfRef
	kSecMatchLimit                 cfRef
	kSecMatchLimitOne              cfRef
	kSecUseAuthenticationUI        cfRef
	kSecUseAuthenticationUIFail    cfRef
	kCFBooleanTrue                 cfRef
	kCFBooleanFalse                cfRef

	// Unlike the rest, these two are structs: what CFDictionaryCreate wants is
	// the address of the global, not a reference read out of it.
	kCFTypeDictionaryKeyCallBacks   uintptr
	kCFTypeDictionaryValueCallBacks uintptr
)

// binding names one C function and the Go variable that will call it.
type binding struct {
	dest any
	name string
}

// constant names one C global and where its address, or the reference at that
// address, lands.
type constant struct {
	dest *cfRef
	name string
}

var securityFunctions = []binding{
	{&secItemAdd, "SecItemAdd"},
	{&secItemCopyMatching, "SecItemCopyMatching"},
	{&secItemUpdate, "SecItemUpdate"},
	{&secItemDelete, "SecItemDelete"},
	{&secAllowInteraction, "SecKeychainSetUserInteractionAllowed"},
}

var coreFoundationFunctions = []binding{
	{&cfRelease, "CFRelease"},
	{&cfStringCreate, "CFStringCreateWithCString"},
	{&cfDataCreate, "CFDataCreate"},
	{&cfDataGetLength, "CFDataGetLength"},
	{&cfDataGetBytePtr, "CFDataGetBytePtr"},
	{&cfDictionaryCreate, "CFDictionaryCreateMutable"},
	{&cfDictionarySetValue, "CFDictionarySetValue"},
}

var securityConstants = []constant{
	{&kSecClass, "kSecClass"},
	{&kSecClassGenericPassword, "kSecClassGenericPassword"},
	{&kSecAttrService, "kSecAttrService"},
	{&kSecAttrAccount, "kSecAttrAccount"},
	{&kSecAttrSynchronizable, "kSecAttrSynchronizable"},
	{&kSecAttrAccessible, "kSecAttrAccessible"},
	{&kSecAttrAccessibleWhenUnlocked, "kSecAttrAccessibleWhenUnlocked"},
	{&kSecValueData, "kSecValueData"},
	{&kSecReturnData, "kSecReturnData"},
	{&kSecMatchLimit, "kSecMatchLimit"},
	{&kSecMatchLimitOne, "kSecMatchLimitOne"},
	{&kSecUseAuthenticationUI, "kSecUseAuthenticationUI"},
	{&kSecUseAuthenticationUIFail, "kSecUseAuthenticationUIFail"},
}

var coreFoundationConstants = []constant{
	{&kCFBooleanTrue, "kCFBooleanTrue"},
	{&kCFBooleanFalse, "kCFBooleanFalse"},
}

// loadSecurity opens both frameworks and resolves everything above, once per
// process. purego panics on a symbol it cannot find, so every name is looked up
// here first: a command can report an error, and nothing panics mid-keychain.
var loadSecurity = sync.OnceValue(func() error {
	security, err := open(securityPath)
	if err != nil {
		return err
	}
	coreFoundation, err := open(coreFoundationPath)
	if err != nil {
		return err
	}

	security.functions(securityFunctions)
	security.references(securityConstants)
	coreFoundation.functions(coreFoundationFunctions)
	coreFoundation.references(coreFoundationConstants)
	kCFTypeDictionaryKeyCallBacks = coreFoundation.address("kCFTypeDictionaryKeyCallBacks")
	kCFTypeDictionaryValueCallBacks = coreFoundation.address("kCFTypeDictionaryValueCallBacks")

	if err := errors.Join(security.err, coreFoundation.err); err != nil {
		return fmt.Errorf("the OS keychain is unreachable: %w", err)
	}
	return nil
})

// framework is one library opened at run time. A failed lookup accumulates on
// err instead of returning, so the loader above reads as the list of what dbmap
// needs and one report names every symbol that is missing.
type framework struct {
	path   string
	handle uintptr
	err    error
}

func open(path string) (*framework, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("the OS keychain is unreachable: loading %s: %w", path, err)
	}
	return &framework{path: path, handle: handle}, nil
}

// functions points each Go variable at the C function of the same name.
func (f *framework) functions(bindings []binding) {
	for _, b := range bindings {
		if symbol := f.address(b.name); symbol != 0 {
			purego.RegisterFunc(b.dest, symbol)
		}
	}
}

// references reads a CFTypeRef global. The symbol is the variable, so its
// address is dereferenced once here and the reference reused afterwards.
func (f *framework) references(constants []constant) {
	for _, c := range constants {
		if symbol := f.address(c.name); symbol != 0 {
			*c.dest = refAt(symbol)
		}
	}
}

// refAt reads the reference stored at a symbol's address. It goes through the
// address of symbol rather than converting the uintptr to an unsafe.Pointer
// directly, which is the pattern that would be unsound for Go memory: what is
// read here is a framework global, at a fixed address for the life of the
// process.
func refAt(symbol uintptr) cfRef {
	return **(**cfRef)(unsafe.Pointer(&symbol))
}

// address is the symbol's address, or zero with the reason recorded.
func (f *framework) address(name string) uintptr {
	symbol, err := purego.Dlsym(f.handle, name)
	if err != nil {
		f.err = errors.Join(f.err, fmt.Errorf("%s does not export %s: %w", f.path, name, err))
		return 0
	}
	return symbol
}
