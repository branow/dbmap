//go:build !darwin || !cgo

package credentials

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Every platform but macOS reaches its credential store through go-keyring:
// wincred on Windows, secret-service on Linux. macOS uses it too when cgo is
// off, which is the only build where dbmap cannot call the Security framework
// in its own process.
//
// The allow decision is recorded by the store above and cannot be honoured
// here, because go-keyring exposes no way to refuse a dialog. These backends do
// not tie an item to the calling binary's code identity, so they do not raise
// the dialog that motivated the flag.
func itemGet(service, account string, _ ui) (string, error) {
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errMissing
	}
	return value, err
}

func itemSet(service, account, secret string, _ ui) error {
	return keyring.Set(service, account, secret)
}

func itemDelete(service, account string, _ ui) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return errMissing
	}
	return err
}
